#!/usr/bin/env bash
# 以 Docker 容器方式启动本服务
#
# 用法：
#   run.sh [--tag <tag>] [--port <hostPort>] [--container-port <port>] [--detach] [--mount-config]
#
# 说明：
#   - 容器以宿主当前用户（非 root）运行，日志与数据目录映射到 <service>/logs 与 <service>/data
#   - 默认前台运行；--detach 后台运行（容器名 ganrag-<service>）
#   - 镜像内默认已复制配置；--mount-config 时用宿主机 configs/config.yaml 覆盖
#   - 服务加入 ganrag 自定义网络，便于服务间按容器名互访
#   - 服务尚未实现时提示并跳过
set -euo pipefail

# 脚本所在目录：service/<name>/scripts
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# 服务目录：service/<name>
SERVICE_DIR="$(dirname "${SCRIPT_DIR}")"
SERVICE="$(basename "${SERVICE_DIR}")"

TAG="latest"
HOST_PORT=""
CONTAINER_PORT="50053"
DETACH="false"
MOUNT_CONFIG="false"
while [ $# -gt 0 ]; do
  case "$1" in
  --tag)
    TAG="$2"
    shift 2
    ;;
  --port)
    HOST_PORT="$2"
    shift 2
    ;;
  --container-port)
    CONTAINER_PORT="$2"
    shift 2
    ;;
  --detach)
    DETACH="true"
    shift
    ;;
  --mount-config)
    MOUNT_CONFIG="true"
    shift
    ;;
  *)
    echo "未知参数：$1" >&2
    exit 1
    ;;
  esac
done

# 尚未实现的服务：提示并跳过（退出码 0，不阻断批量启动）
if ! compgen -G "${SERVICE_DIR}/cmd/server/*.go" >/dev/null; then
  echo "跳过 ${SERVICE}：尚未实现（缺少 service/${SERVICE}/cmd/server/*.go）"
  exit 0
fi

IMAGE="ganrag/${SERVICE}:${TAG}"
if ! docker image inspect "${IMAGE}" >/dev/null 2>&1; then
  echo "镜像 ${IMAGE} 不存在，请先执行：task build-image" >&2
  exit 1
fi

# 宿主机数据/日志目录（映射进容器，属主为当前用户，保证非 root 容器用户可写）
mkdir -p "${SERVICE_DIR}/data" "${SERVICE_DIR}/logs"

# 服务间互访网络（按需创建）
NETWORK="ganrag"
if ! docker network inspect "${NETWORK}" >/dev/null 2>&1; then
  docker network create "${NETWORK}" >/dev/null
fi

# 后台运行时先清理同名容器，保证可重复启动
if [ "${DETACH}" = "true" ]; then
  docker rm -f "ganrag-${SERVICE}" >/dev/null 2>&1 || true
fi

RUN_ARGS=(
  --rm
  --network "${NETWORK}"
  --user "$(id -u):$(id -g)"
  --volume "${SERVICE_DIR}/data:/app/data"
  --volume "${SERVICE_DIR}/logs:/app/logs"
)
if [ "${DETACH}" = "true" ]; then
  RUN_ARGS+=(--detach --name "ganrag-${SERVICE}")
fi
if [ -n "${HOST_PORT}" ]; then
  RUN_ARGS+=(--publish "${HOST_PORT}:${CONTAINER_PORT}")
fi
if [ "${MOUNT_CONFIG}" = "true" ] && [ -f "${SERVICE_DIR}/configs/config.yaml" ]; then
  RUN_ARGS+=(--volume "${SERVICE_DIR}/configs/config.yaml:/app/configs/config.yaml:ro")
fi

echo "启动 ${SERVICE}（镜像 ${IMAGE}）"
docker run "${RUN_ARGS[@]}" "${IMAGE}"

if [ "${DETACH}" = "true" ]; then
  echo "已后台启动：ganrag-${SERVICE}（日志：docker logs -f ganrag-${SERVICE}；停止：task stop）"
fi
