#!/usr/bin/env bash
# 构建本服务的 Docker 镜像
#
# 用法：
#   build-image.sh [--tag <tag>] [--os <goos>] [--arch <goarch>] [--copy-config true|false]
#
# 说明：
#   - 仅支持 linux 二进制（容器运行环境）
#   - 二进制来自同目录 scripts/build.sh 的产物：<service>/bin/server-<os>-<arch>
#   - --copy-config 控制是否把 <service>/configs 复制进镜像（默认 true）
#   - 服务尚未实现时提示并跳过
set -euo pipefail

# 脚本所在目录：service/<name>/scripts
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# 服务目录：service/<name>
SERVICE_DIR="$(dirname "${SCRIPT_DIR}")"
SERVICE="$(basename "${SERVICE_DIR}")"

TAG="latest"
OS="linux"
ARCH="amd64"
COPY_CONFIG="true"
while [ $# -gt 0 ]; do
  case "$1" in
  --tag)
    TAG="$2"
    shift 2
    ;;
  --os)
    OS="$2"
    shift 2
    ;;
  --arch)
    ARCH="$2"
    shift 2
    ;;
  --copy-config)
    COPY_CONFIG="$2"
    shift 2
    ;;
  *)
    echo "未知参数：$1" >&2
    exit 1
    ;;
  esac
done

if [ "${OS}" != "linux" ]; then
  echo "Docker 镜像仅支持 linux 二进制（当前 --os ${OS}）" >&2
  exit 1
fi

# 尚未实现的服务：提示并跳过（退出码 0，不阻断批量构建）
if ! compgen -G "${SERVICE_DIR}/cmd/server/*.go" >/dev/null; then
  echo "跳过 ${SERVICE}：尚未实现（缺少 service/${SERVICE}/cmd/server/*.go）"
  exit 0
fi

BINARY="server-${OS}-${ARCH}"
if [ ! -f "${SERVICE_DIR}/bin/${BINARY}" ]; then
  echo "未找到二进制 service/${SERVICE}/bin/${BINARY}，请先执行：task build" >&2
  exit 1
fi

IMAGE="ganrag/${SERVICE}:${TAG}"
echo "构建镜像 ${IMAGE}（BINARY=${BINARY} COPY_CONFIG=${COPY_CONFIG}）"

# 构建参数：
#   APP_UID/APP_GID：容器内运行用户的 UID/GID，默认与宿主当前用户一致，保证挂载目录可写
#   BINARY：要复制进镜像的二进制文件名
#   COPY_CONFIG：是否把 configs 目录复制进镜像（false 时依赖运行时挂载）
docker build \
  -f "${SERVICE_DIR}/Dockerfile" \
  -t "${IMAGE}" \
  --build-arg "APP_UID=$(id -u)" \
  --build-arg "APP_GID=$(id -g)" \
  --build-arg "BINARY=${BINARY}" \
  --build-arg "COPY_CONFIG=${COPY_CONFIG}" \
  "${SERVICE_DIR}"

echo "镜像构建完成：${IMAGE}"
