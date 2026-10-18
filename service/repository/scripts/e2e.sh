#!/usr/bin/env bash
# Repository 阶段一真实环境冒烟测试
#
# 覆盖：知识库 CRUD、权限（公开/私有）、整段上传 sha256、删除时 async_tasks outbox。
# 前置：task infra（MySQL + Garage）、仓库根 .env、grpcurl、python3。
# 用法：./scripts/e2e.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_DIR="$(dirname "${SCRIPT_DIR}")"
cd "${SERVICE_DIR}"

ADDR="localhost:50052"
USER_XID="usr_dba6jv2kpl5qdid12uhg"
OTHER_USER_XID="usr_dba6jv2kpl5qdid12ui0"
NAME_PREFIX="e2e_repo_$(date +%s)"
WORK_DIR="$(mktemp -d /tmp/opencode/repository-e2e.XXXXXX 2>/dev/null || mktemp -d)"
ERR_FILE="${WORK_DIR}/grpc-err.txt"
SERVER_LOG="${WORK_DIR}/server.log"
SERVER_PID=""
PASS=0
FAIL=0

fatal() { echo "✘ 致命错误：$1" >&2; [ -f "${SERVER_LOG}" ] && tail -30 "${SERVER_LOG}" >&2; exit 1; }
pass() { PASS=$((PASS + 1)); echo "  ✔ $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  ✘ $1"; [ -n "${2:-}" ] && echo "    诊断：$2"; }

call() { OUT="$(grpcurl -plaintext -connect-timeout 5 -max-time 30 -d "$2" "${ADDR}" "$1" 2>"${ERR_FILE}")" && RC=0 || RC=$?; }
assert_ok() { call "$2" "$3"; if [ "${RC}" -eq 0 ]; then pass "$1"; else fail "$1" "stderr=$(tr '\n' ' ' <"${ERR_FILE}")"; fi; }
assert_err() { call "$2" "$3"; if [ "${RC}" -ne 0 ] && grep -Eq "Code: *(DataLoss|9)" "${ERR_FILE}"; then pass "$1"; else fail "$1" "stderr=$(tr '\n' ' ' <"${ERR_FILE}")"; fi; }
jget() { printf '%s' "${OUT}" | python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
gen_xid() { python3 -c 'import secrets; a="0123456789abcdefghijklmnopqrstuv"; print("".join(secrets.choice(a) for _ in range(20)))'; }
mysql_exec() { docker exec -i ganrag-mysql mysql -u"${GANRAG_DB_USER:-ganrag}" -p"${GANRAG_DB_PASSWORD}" "$1" -N -e "$2" 2>/dev/null; }

cleanup() {
  [ -n "${SERVER_PID}" ] && { kill "${SERVER_PID}" >/dev/null 2>&1 || true; wait "${SERVER_PID}" 2>/dev/null || true; }
  if [ -n "${GANRAG_DB_PASSWORD:-}" ]; then
    mysql_exec ganrag_repository "DELETE FROM knowledge_bases WHERE name LIKE '${NAME_PREFIX}%';" || true
    mysql_exec ganrag_common "DELETE FROM async_tasks WHERE source_service='repository' AND requested_by_xid='${USER_XID}' AND created_at >= NOW() - INTERVAL 10 MINUTE;" || true
  fi
}
trap cleanup EXIT

command -v grpcurl >/dev/null || fatal "未安装 grpcurl"
command -v python3 >/dev/null || fatal "未安装 python3"
docker ps --format '{{.Names}}' | grep -qx 'ganrag-mysql' || fatal "MySQL 未运行，请先 task infra"
docker ps --format '{{.Names}}' | grep -qx 'ganrag-garage' || fatal "Garage 未运行，请先 task infra"

ROOT_ENV="$(cd "${SERVICE_DIR}/../.." && pwd)/.env"
[ -f "${ROOT_ENV}" ] || fatal "缺少仓库根 .env"
# shellcheck disable=SC1090
set -a && . "${ROOT_ENV}" && set +a

if (exec 3<>"/dev/tcp/127.0.0.1/50052") 2>/dev/null; then fatal "50052 端口已被占用"; fi

echo "== 编译并启动服务 =="
go build -o "${WORK_DIR}/repository-server" ./cmd/server || fatal "编译失败"
"${WORK_DIR}/repository-server" >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!

ready="false"
for _ in $(seq 1 60); do
  kill -0 "${SERVER_PID}" 2>/dev/null || fatal "服务进程已退出"
  if grpcurl -plaintext -connect-timeout 1 -max-time 2 -d '{}' "${ADDR}" grpc.health.v1.Health/Check >/dev/null 2>&1; then ready="true"; break; fi
  sleep 1
done
[ "${ready}" = "true" ] || fatal "等待服务就绪超时"

echo "== 知识库 =="
call repository.v1.RepositoryService/CreateKnowledgeBase "{\"user_xid\":\"${USER_XID}\",\"name\":\"${NAME_PREFIX}_kb\",\"visibility\":0}"
[ "${RC}" -eq 0 ] || fatal "创建知识库失败：$(cat "${ERR_FILE}")"
KB_XID="$(jget "d['knowledgeBase']['xid']")"
[ -n "${KB_XID}" ] && pass "创建知识库 ${KB_XID}" || fail "创建知识库"

assert_ok "owner 查询私有库" repository.v1.RepositoryService/GetKnowledgeBase "{\"user_xid\":\"${USER_XID}\",\"kb_xid\":\"${KB_XID}\"}"
call repository.v1.RepositoryService/GetKnowledgeBase "{\"user_xid\":\"${OTHER_USER_XID}\",\"kb_xid\":\"${KB_XID}\"}"
if [ "${RC}" -ne 0 ] && grep -q 'NotFound' "${ERR_FILE}"; then pass "非 owner 读私有库 NotFound"; else fail "非 owner 读私有库 NotFound"; fi

# 整段上传
DATA_B64="aGVsbG8gcmVwb3NpdG9yeQ=="
SHA="$(printf 'hello repository' | sha256sum | cut -d' ' -f1)"
call repository.v1.RepositoryService/UploadWhole "{\"user_xid\":\"${USER_XID}\",\"content_type\":\"text/plain\",\"original_name\":\"e2e.txt\",\"data\":\"${DATA_B64}\",\"sha256\":\"${SHA}\"}"
[ "${RC}" -eq 0 ] || fatal "上传失败：$(cat "${ERR_FILE}")"
OBJ_XID="$(jget "d['result']['objectXid']")"
[ "$(jget "d['result']['sha256']")" = "${SHA}" ] && pass "UploadWhole ${OBJ_XID}" || fail "UploadWhole sha256"

assert_err "错误 sha256 返回 DataLoss" repository.v1.RepositoryService/UploadWhole \
  "{\"user_xid\":\"${USER_XID}\",\"data\":\"${DATA_B64}\",\"sha256\":\"$(printf '0%.0s' {1..64})\"}"

# 挂一篇正文引用上传对象，再删除知识库，验证 outbox 清理
ART_XID="art_$(gen_xid)"
mysql_exec ganrag_repository "INSERT INTO articles (xid,kb_id,kb_xid,dir_id,dir_xid,type,title,content_key,content_object_xid,created_by_xid) SELECT '${ART_XID}',id,xid,0,'',1,'e2e','o/${OBJ_XID}','${OBJ_XID}','${USER_XID}' FROM knowledge_bases WHERE xid='${KB_XID}';" || fatal "插入测试文章失败"

assert_ok "删除知识库" repository.v1.RepositoryService/DeleteKnowledgeBase "{\"user_xid\":\"${USER_XID}\",\"kb_xid\":\"${KB_XID}\"}"
TASK_STATUS="$(mysql_exec ganrag_common "SELECT status FROM async_tasks WHERE source_service='repository' AND task_type='garage_object_delete' AND dedupe_key='${OBJ_XID}' ORDER BY id DESC LIMIT 1;")"
[ "${TASK_STATUS}" = "1" ] && pass "async_tasks 任务已解决（status=1）" || fail "async_tasks 状态 = ${TASK_STATUS}，期望 1"
ART_COUNT="$(mysql_exec ganrag_repository "SELECT COUNT(*) FROM articles WHERE xid='${ART_XID}';")"
[ "${ART_COUNT}" = "0" ] && pass "文章级联删除" || fail "文章未删除（count=${ART_COUNT}）"

echo ""
echo "== 结果：通过 ${PASS} / 失败 ${FAIL} =="
[ "${FAIL}" -eq 0 ] || exit 1
