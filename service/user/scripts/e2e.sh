#!/usr/bin/env bash
# User 服务真实环境接口冒烟测试
#
# 与 go test（bufconn 内存测试）互补：本脚本走「真实 MySQL + 真实迁移 + 真实配置
# 加载 + TCP 监听」的完整链路，用 grpcurl 调用全部 5 个 RPC 与健康检查、服务反射。
#
# 用法：
#   ./scripts/e2e.sh            # 在 service/user 目录下执行
#
# 前置条件：
#   - task infra 已启动 MySQL（容器 ganrag-mysql）
#   - service/user/.env 已配置（GANRAG_DATABASE_PASSWORD、GANRAG_JWT_SECRET）
#   - 已安装 grpcurl 与 python3
#   - 50051 端口空闲（脚本自行编译并启动服务，结束自动停止）
#
# 测试数据：用户名统一 e2e_<时间戳> 前缀，结束时从数据库整体清理。
set -euo pipefail

# 脚本所在目录与服务目录（服务配置 configs/config.yaml、.env 均按服务目录相对解析）
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_DIR="$(dirname "${SCRIPT_DIR}")"
cd "${SERVICE_DIR}"

# 被测服务地址与测试数据前缀
ADDR="localhost:50051"
USER_PREFIX="e2e_$(date +%s)"

# 工作目录：编译产物、服务日志、grpcurl 错误输出（/tmp/opencode 为外部访问预留目录）
WORK_DIR="$(mktemp -d /tmp/opencode/user-e2e.XXXXXX 2>/dev/null || mktemp -d)"
ERR_FILE="${WORK_DIR}/grpc-err.txt"
SERVER_LOG="${WORK_DIR}/server.log"
SERVER_PID=""

# 断言计数
PASS=0
FAIL=0

# ---------- 辅助函数 ----------

# fatal <消息>：前置条件不满足或关键步骤失败时终止测试
fatal() {
  echo "✘ 致命错误：$1" >&2
  [ -f "${SERVER_LOG}" ] && { echo "--- 服务日志尾部 ---" >&2; tail -30 "${SERVER_LOG}" >&2; }
  exit 1
}

# pass <消息>：记录一条通过的断言
pass() {
  PASS=$((PASS + 1))
  echo "  ✔ $1"
}

# fail <消息> [诊断输出]：记录一条失败的断言并打印诊断
fail() {
  FAIL=$((FAIL + 1))
  echo "  ✘ $1"
  [ -n "${2:-}" ] && printf '    诊断：%s\n' "$2"
}

# call <method> <json>：执行一次 grpcurl 调用，结果存入 $OUT（stdout）与 $RC（退出码）
# 错误输出重定向到 $ERR_FILE 供断言与诊断使用
call() {
  OUT="$(grpcurl -plaintext -connect-timeout 5 -max-time 30 -d "$2" "${ADDR}" "$1" 2>"${ERR_FILE}")" && RC=0 || RC=$?
}

# assert_ok <描述> <method> <json>：断言调用成功（gRPC 返回 OK）
assert_ok() {
  call "$2" "$3"
  if [ "${RC}" -eq 0 ]; then
    pass "$1"
  else
    fail "$1" "退出码=${RC}，stderr=$(tr '\n' ' ' <"${ERR_FILE}")"
  fi
}

# assert_err <描述> <错误码名> <method> <json>：断言调用失败且 gRPC 错误码匹配
# 同时匹配错误码名称（grpcurl 输出 Code: Name）与数字，兼容不同输出格式
declare -A CODE_NUM=(
  [InvalidArgument]=3 [NotFound]=5 [AlreadyExists]=6
  [FailedPrecondition]=9 [Unauthenticated]=16
)
assert_err() {
  local desc="$1" code="$2" num="${CODE_NUM[$2]:-0}"
  call "$3" "$4"
  if [ "${RC}" -ne 0 ] && grep -Eq "Code: *(${code}|${num})([^0-9A-Za-z]|\$)" "${ERR_FILE}"; then
    pass "${desc}（${code}）"
  else
    fail "${desc}（期望 ${code}）" "退出码=${RC}，stderr=$(tr '\n' ' ' <"${ERR_FILE}")"
  fi
}

# last_err_message：取出最近一次失败调用的 Message 文本（防枚举断言用）
last_err_message() {
  sed -n 's/^.*Message: *//p' "${ERR_FILE}" | head -1
}

# jget <python 表达式>：以最近一次成功的 JSON 响应为 d 求值并打印结果
jget() {
  printf '%s' "${OUT}" | python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"
}

# verify_jwt <token> <secret> <sub> <username>：HMAC-SHA256 验签并核对载荷
# 模拟 Gateway 本地验签契约：密钥一致、sub 为用户 ID、username 声明正确
verify_jwt() {
  python3 - "$1" "$2" "$3" "$4" <<'PY'
import base64, hashlib, hmac, json, sys

token, secret, want_sub, want_username = sys.argv[1:5]
header, payload, sig = token.split(".")

def b64url_decode(s):
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))

# 验签：签名 = base64url(HMAC-SHA256(header.payload, secret))
expected = base64.urlsafe_b64encode(
    hmac.new(secret.encode(), f"{header}.{payload}".encode(), hashlib.sha256).digest()
).rstrip(b"=").decode()
assert sig == expected, "签名不匹配"

claims = json.loads(b64url_decode(payload))
assert claims["sub"] == want_sub, f"sub={claims['sub']!r}，期望 {want_sub!r}"
assert claims["username"] == want_username, f"username={claims['username']!r}"
assert claims["iss"], "缺少 iss 声明"
assert claims["exp"] > claims["iat"], "exp 应晚于 iat"
print("OK")
PY
}

# mysql_exec <SQL>：通过容器内 mysql 客户端在 ganrag_user 库执行 SQL（禁用与清理用）
mysql_exec() {
  docker exec -i ganrag-mysql mysql -u"${GANRAG_DB_USER:-ganrag}" -p"${GANRAG_DB_PASSWORD}" ganrag_user \
    -e "$1" 2>/dev/null
}

# cleanup：结束时停止被测服务并清理测试用户（由 trap 调用）
cleanup() {
  if [ -n "${SERVER_PID}" ]; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  if [ -n "${GANRAG_DB_PASSWORD:-}" ]; then
    mysql_exec "DELETE FROM users WHERE username LIKE '${USER_PREFIX}%'" \
      || echo "警告：清理测试用户失败（不影响测试结论）" >&2
  fi
}

# ---------- 前置检查 ----------

echo "== 前置检查 =="
command -v grpcurl >/dev/null || fatal "未安装 grpcurl（go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest）"
command -v python3 >/dev/null || fatal "未安装 python3"
command -v docker >/dev/null || fatal "未安装 docker"
docker ps --format '{{.Names}}' | grep -qx 'ganrag-mysql' || fatal "MySQL 容器未运行，请先执行：task infra（仓库根目录）"

# 读取 .env：仓库根（数据库账号密码，供禁用与清理用）+ 服务目录（JWT 密钥，验签用）
ROOT_ENV="$(cd "${SERVICE_DIR}/../.." && pwd)/.env"
[ -f "${ROOT_ENV}" ] || fatal "缺少仓库根 .env（cp .env.example .env 后填写密码）"
[ -f "${SERVICE_DIR}/.env" ] || fatal "缺少 service/user/.env（配置 GANRAG_DATABASE_PASSWORD 与 GANRAG_JWT_SECRET）"
# shellcheck disable=SC1090
set -a && . "${ROOT_ENV}" && . "${SERVICE_DIR}/.env" && set +a

# 50051 端口预检：已被占用则拒绝测试，避免误杀用户自己的服务
if (exec 3<>"/dev/tcp/127.0.0.1/50051") 2>/dev/null; then
  fatal "50051 端口已被占用，请先停止占用服务再执行本脚本"
fi

# ---------- 启动被测服务 ----------

echo "== 编译并启动服务 =="
go build -o "${WORK_DIR}/user-server" ./cmd/server || fatal "编译失败"
"${WORK_DIR}/user-server" >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!
trap cleanup EXIT

# 等待健康检查就绪（最多 60 秒；服务进程提前退出则立即终止）
ready="false"
for _ in $(seq 1 60); do
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    fatal "服务进程已退出"
  fi
  if grpcurl -plaintext -connect-timeout 1 -max-time 2 -d '{}' \
      "${ADDR}" grpc.health.v1.Health/Check >/dev/null 2>&1; then
    ready="true"
    break
  fi
  sleep 1
done
[ "${ready}" = "true" ] || fatal "等待服务就绪超时（60 秒）"
echo "服务已就绪：${ADDR}"

# ---------- 用例：反射与健康检查 =="
echo "== 服务反射 / 健康检查 =="
if grpcurl -plaintext "${ADDR}" list >"${WORK_DIR}/services.txt" 2>"${ERR_FILE}"; then
  if grep -q 'user.v1.UserService' "${WORK_DIR}/services.txt"; then
    pass "反射列表包含 user.v1.UserService"
  else
    fail "反射列表包含 user.v1.UserService" "实际：$(tr '\n' ' ' <"${WORK_DIR}/services.txt")"
  fi
else
  fail "服务反射 list" "$(tr '\n' ' ' <"${ERR_FILE}")"
fi
assert_ok "健康检查返回 SERVING" grpc.health.v1.Health/Check '{}'
if [ "${RC}" -eq 0 ] && printf '%s' "${OUT}" | grep -q 'SERVING'; then
  pass "健康状态为 SERVING"
else
  fail "健康状态为 SERVING" "实际：${OUT}"
fi

# ---------- 用例：注册 ----------
echo "== Register =="
UNAME="${USER_PREFIX}_u1"

call user.v1.UserService/Register "{\"username\":\"${UNAME}\",\"password\":\"password123\"}"
if [ "${RC}" -ne 0 ]; then
  fatal "注册测试用户失败：$(tr '\n' ' ' <"${ERR_FILE}")"
fi
USER_ID="$(jget "d['user']['id']")"
if [ -n "${USER_ID}" ] && [ "${USER_ID}" -gt 0 ] \
  && [ "$(jget "d['user']['username']")" = "${UNAME}" ] \
  && [ "$(jget "d['user']['nickname']")" = "${UNAME}" ] \
  && [ "$(jget "d['user']['status']")" = "1" ] \
  && [ "$(jget "d['user']['createdAtUnix']")" -gt 0 ]; then
  pass "注册成功且返回字段完整（id=${USER_ID}）"
else
  fail "注册成功且返回字段完整" "实际：${OUT}"
fi

assert_err "重复用户名被拒绝" AlreadyExists user.v1.UserService/Register \
  "{\"username\":\"${UNAME}\",\"password\":\"password123\"}"
assert_err "非法用户名被拒绝" InvalidArgument user.v1.UserService/Register \
  '{"username":"bad name!","password":"password123"}'
assert_err "过短密码被拒绝" InvalidArgument user.v1.UserService/Register \
  "{\"username\":\"${USER_PREFIX}_u2\",\"password\":\"short1\"}"

# ---------- 用例：登录 ----------
echo "== Login =="
call user.v1.UserService/Login "{\"username\":\"${UNAME}\",\"password\":\"password123\"}"
if [ "${RC}" -ne 0 ]; then
  fail "登录成功" "$(tr '\n' ' ' <"${ERR_FILE}")"
  ACCESS=""; REFRESH=""
else
  ACCESS="$(jget "d['accessToken']")"
  REFRESH="$(jget "d['refreshToken']")"
  if [ -n "${ACCESS}" ] && [ -n "${REFRESH}" ] \
    && [ "$(jget "d['accessExpiresIn']")" = "900" ] \
    && [ "$(jget "d['refreshExpiresIn']")" = "604800" ]; then
    pass "登录返回双 token 与有效期（900/604800 秒）"
  else
    fail "登录返回双 token 与有效期" "实际：${OUT}"
  fi
  # Gateway 契约：共享密钥本地验签 + sub/username 载荷
  if [ -n "${GANRAG_JWT_SECRET:-}" ]; then
    if verify_jwt "${ACCESS}" "${GANRAG_JWT_SECRET}" "${USER_ID}" "${UNAME}" >/dev/null 2>&1; then
      pass "access token 验签通过且 sub/username 正确（Gateway 契约）"
    else
      fail "access token 验签通过且 sub/username 正确" "err=$(verify_jwt "${ACCESS}" "${GANRAG_JWT_SECRET}" "${USER_ID}" "${UNAME}" 2>&1 | tail -1)"
    fi
  else
    fail "access token 验签（缺少 GANRAG_JWT_SECRET）" "service/user/.env 未配置"
  fi
fi

# 防用户名枚举：不存在用户与密码错误必须返回同一消息
call user.v1.UserService/Login "{\"username\":\"ghost_${USER_PREFIX}\",\"password\":\"password123\"}"
if [ "${RC}" -ne 0 ] && grep -q 'Unauthenticated' "${ERR_FILE}"; then
  GHOST_MSG="$(last_err_message)"
  pass "不存在用户返回 Unauthenticated"
else
  GHOST_MSG=""
  fail "不存在用户返回 Unauthenticated" "退出码=${RC}，stderr=$(tr '\n' ' ' <"${ERR_FILE}")"
fi
call user.v1.UserService/Login "{\"username\":\"${UNAME}\",\"password\":\"wrong-pass-1\"}"
if [ "${RC}" -ne 0 ] && grep -q 'Unauthenticated' "${ERR_FILE}"; then
  pass "密码错误返回 Unauthenticated"
else
  fail "密码错误返回 Unauthenticated" "退出码=${RC}"
fi
WRONG_MSG="$(last_err_message)"
if [ -n "${GHOST_MSG}" ] && [ "${GHOST_MSG}" = "${WRONG_MSG}" ]; then
  pass "防枚举：两种失败消息一致（${GHOST_MSG}）"
else
  fail "防枚举：两种失败消息一致" "不存在='${GHOST_MSG}' 密码错='${WRONG_MSG}'"
fi

assert_err "空参数登录被拒绝" InvalidArgument user.v1.UserService/Login '{"username":"","password":""}'

# ---------- 用例：查询用户 ----------
echo "== GetUser =="
assert_ok "按 ID 查询成功" user.v1.UserService/GetUser "{\"id\":${USER_ID}}"
if [ "${RC}" -eq 0 ] && [ "$(jget "d['user']['username']")" = "${UNAME}" ]; then
  pass "查询结果用户名匹配"
else
  fail "查询结果用户名匹配" "实际：${OUT}"
fi
assert_err "缺少 ID 被拒绝" InvalidArgument user.v1.UserService/GetUser '{"id":0}'
assert_err "不存在的用户返回 NotFound" NotFound user.v1.UserService/GetUser '{"id":999999999}'

# ---------- 用例：改密（先于刷新用例，验证改密吊销全部 refresh） ----------
echo "== UpdatePassword =="
assert_err "缺少 user_id 被拒绝" InvalidArgument user.v1.UserService/UpdatePassword \
  '{"user_id":0,"old_password":"password123","new_password":"new-pass-456"}'
assert_err "新密码过短被拒绝" InvalidArgument user.v1.UserService/UpdatePassword \
  "{\"user_id\":${USER_ID},\"old_password\":\"password123\",\"new_password\":\"short\"}"
assert_err "旧密码错误返回 Unauthenticated" Unauthenticated user.v1.UserService/UpdatePassword \
  "{\"user_id\":${USER_ID},\"old_password\":\"wrong-pass-1\",\"new_password\":\"new-pass-456\"}"
assert_ok "改密成功" user.v1.UserService/UpdatePassword \
  "{\"user_id\":${USER_ID},\"old_password\":\"password123\",\"new_password\":\"new-pass-456\"}"

# 改密吊销：改密前签发的 refresh 应失效
assert_err "改密后旧 refresh 被吊销" Unauthenticated user.v1.UserService/Refresh \
  "{\"refresh_token\":\"${REFRESH}\"}"
# 旧密码作废、新密码可登录
assert_err "旧密码登录被拒绝" Unauthenticated user.v1.UserService/Login \
  "{\"username\":\"${UNAME}\",\"password\":\"password123\"}"
call user.v1.UserService/Login "{\"username\":\"${UNAME}\",\"password\":\"new-pass-456\"}"
if [ "${RC}" -eq 0 ]; then
  REFRESH="$(jget "d['refreshToken']")"
  pass "新密码登录成功"
else
  REFRESH=""
  fail "新密码登录成功" "stderr=$(tr '\n' ' ' <"${ERR_FILE}")"
fi

# ---------- 用例：刷新轮换与重用检测 ----------
echo "== Refresh =="
if [ -n "${REFRESH}" ]; then
  call user.v1.UserService/Refresh "{\"refresh_token\":\"${REFRESH}\"}"
  if [ "${RC}" -eq 0 ]; then
    ROTATED="$(jget "d['refreshToken']")"
    if [ -n "${ROTATED}" ] && [ "${ROTATED}" != "${REFRESH}" ]; then
      pass "轮换签发全新 refresh token"
    else
      fail "轮换签发全新 refresh token" "实际未变化"
      ROTATED=""
    fi
    if [ -n "${GANRAG_JWT_SECRET:-}" ] \
      && verify_jwt "$(jget "d['accessToken']")" "${GANRAG_JWT_SECRET}" "${USER_ID}" "${UNAME}" >/dev/null 2>&1; then
      pass "刷新返回的新 access token 验签通过"
    else
      fail "刷新返回的新 access token 验签通过"
    fi
  else
    ROTATED=""
    fail "刷新轮换成功" "stderr=$(tr '\n' ' ' <"${ERR_FILE}")"
  fi

  # 重用已作废 token：视为泄露，吊销该用户全部（旧 REFRESH 与新 ROTATED 均应失效）
  assert_err "重用已作废 refresh 返回 Unauthenticated" Unauthenticated user.v1.UserService/Refresh \
    "{\"refresh_token\":\"${REFRESH}\"}"
  if [ -n "${ROTATED}" ]; then
    assert_err "重用检测连带吊销全部 token" Unauthenticated user.v1.UserService/Refresh \
      "{\"refresh_token\":\"${ROTATED}\"}"
  fi
else
  fail "刷新用例（依赖登录成功，已跳过）"
fi
assert_err "伪造 refresh token 被拒绝" Unauthenticated user.v1.UserService/Refresh \
  '{"refresh_token":"forged-token"}'
assert_err "空 refresh token 被拒绝" InvalidArgument user.v1.UserService/Refresh '{"refresh_token":""}'

# ---------- 用例：禁用账号 ----------
echo "== 禁用账号 =="
# 先在启用状态下登录拿有效 refresh，再禁用，分别验证登录与刷新两个分支
DISABLE_RT=""
call user.v1.UserService/Login "{\"username\":\"${UNAME}\",\"password\":\"new-pass-456\"}"
if [ "${RC}" -eq 0 ]; then
  DISABLE_RT="$(jget "d['refreshToken']")"
fi
if mysql_exec "UPDATE users SET status = 0 WHERE id = ${USER_ID}"; then
  assert_err "禁用账号登录返回 FailedPrecondition" FailedPrecondition user.v1.UserService/Login \
    "{\"username\":\"${UNAME}\",\"password\":\"new-pass-456\"}"
  if [ -n "${DISABLE_RT}" ]; then
    assert_err "禁用账号刷新返回 FailedPrecondition" FailedPrecondition user.v1.UserService/Refresh \
      "{\"refresh_token\":\"${DISABLE_RT}\"}"
  else
    fail "禁用账号刷新用例（依赖登录成功，已跳过）"
  fi
  mysql_exec "UPDATE users SET status = 1 WHERE id = ${USER_ID}" || true
else
  fail "禁用账号用例（无法连接 MySQL 执行状态更新）"
fi

# ---------- 汇总 ----------
echo ""
echo "== 结果：通过 ${PASS} / 失败 ${FAIL} =="
if [ "${FAIL}" -gt 0 ]; then
  echo "服务日志：${SERVER_LOG}（失败时日志已随 cleanup 保留于 ${WORK_DIR}）"
  exit 1
fi
echo "全部接口功能用例通过"
