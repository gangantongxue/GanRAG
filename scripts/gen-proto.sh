#!/usr/bin/env bash
# 生成 Vector Store 服务的 gRPC 代码
#
# 依赖（需提前全局安装）：
#   - protoc：protobuf 编译器，位于 PATH 中
#   - protoc-gen-go / protoc-gen-go-grpc：通过 go install 安装，位于 $(go env GOPATH)/bin
#
# 安装命令：
#   sudo apt-get install -y protobuf-compiler   # 或下载 protoc 官方 release 安装到 PATH
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
#
# 生成结果：api/gen/vector-store/v1/*.pb.go（生成代码提交入库，修改 proto 后需重新执行本脚本）
set -euo pipefail

# 项目根目录（脚本位于 scripts/ 目录下）
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# 校验 protoc 是否可用
if ! command -v protoc >/dev/null 2>&1; then
  echo "未找到 protoc，请先全局安装（如 apt-get install protobuf-compiler）" >&2
  exit 1
fi

# go install 安装的插件默认在 GOPATH/bin，需加入 PATH
export PATH="${PATH}:$(go env GOPATH)/bin"

# module 选项：按 go_package 去掉 module 前缀输出，即 api/gen/vector-store/v1/
protoc \
  --proto_path="${ROOT}" \
  --go_out="${ROOT}" \
  --go_opt=module=github.com/gangantongxue/GanRAG \
  --go-grpc_out="${ROOT}" \
  --go-grpc_opt=module=github.com/gangantongxue/GanRAG \
  "${ROOT}/api/proto/vector-store/v1/vector_store.proto"

echo "生成完成：api/gen/vector-store/v1/"
