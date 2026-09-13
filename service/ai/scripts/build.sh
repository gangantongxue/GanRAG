#!/usr/bin/env bash
# 编译本服务的二进制
#
# 用法：
#   build.sh [--os <goos>] [--arch <goarch>]
#
# 约定：
#   - 服务入口位于 <service>/cmd/server
#   - 产物输出到 <service>/bin/server-<goos>-<goarch>
#   - 服务尚未实现（缺少 cmd/server 下的 Go 文件）时提示并跳过，不阻断根 Taskfile 批量编译
set -euo pipefail

# 脚本所在目录：service/<name>/scripts
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# 服务目录：service/<name>
SERVICE_DIR="$(dirname "${SCRIPT_DIR}")"
SERVICE="$(basename "${SERVICE_DIR}")"
# 仓库根目录
ROOT="$(cd "${SERVICE_DIR}/../.." && pwd)"

# 默认编译 linux/amd64（容器运行环境）
OS="linux"
ARCH="amd64"
while [ $# -gt 0 ]; do
  case "$1" in
  --os)
    OS="$2"
    shift 2
    ;;
  --arch)
    ARCH="$2"
    shift 2
    ;;
  *)
    echo "未知参数：$1" >&2
    exit 1
    ;;
  esac
done

MAIN_DIR="${SERVICE_DIR}/cmd/server"

# 尚未实现的服务：提示并跳过（退出码 0，不阻断批量编译）
if ! compgen -G "${MAIN_DIR}/*.go" >/dev/null; then
  echo "跳过 ${SERVICE}：尚未实现（缺少 service/${SERVICE}/cmd/server/*.go）"
  exit 0
fi

OUT="${SERVICE_DIR}/bin/server-${OS}-${ARCH}"
mkdir -p "${SERVICE_DIR}/bin"

echo "编译 ${SERVICE}（GOOS=${OS} GOARCH=${ARCH}）-> service/${SERVICE}/bin/server-${OS}-${ARCH}"
cd "${ROOT}"
CGO_ENABLED=0 GOOS="${OS}" GOARCH="${ARCH}" \
  go build -trimpath -ldflags="-s -w" -o "${OUT}" "./service/${SERVICE}/cmd/server"
