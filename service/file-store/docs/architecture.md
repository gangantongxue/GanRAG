# File Store 服务架构选型

## 概述

自建对象存储服务，使用本地文件夹模拟对象存储，对外暴露 gRPC 接口。

## 技术选型

### 存储方式

**选型：Go 标准库 os 包**

- 零依赖：纯 Go 标准库实现
- 完全可控：无第三方依赖风险
- 简单直接：文件直接存储在本地文件夹

**实现要点：**
- 使用 `os` 包读写文件
- 使用 `filepath` 管理路径
- 自行实现元数据管理（可选 SQLite 或 JSON 文件）

### 通信协议

**选型：gRPC**

- 高性能：适合文件数据的流式传输
- 强类型：Protobuf 定义接口
- 流式传输：支持大文件分块上传/下载

## 职责

- 文件上传
- 文件下载
- 文件删除
- 文件元数据管理
- 目录管理

## 存储目录结构

```
data/
├── bucket1/
│   ├── file1.pdf
│   └── file2.txt
└── bucket2/
    └── file3.docx
```

## 接口设计（已定稿 2026-10-09）

### 设计决策

| 决策点 | 结论 | 原因 |
|--------|------|------|
| 传输方式 | 一元 RPC + 整体 bytes 字段 | 文件以 <10MB 小文件为主，流式属过度设计；未来需要时可新增 `UploadStream` 方法，不破坏现有接口 |
| 同 key 重复上传 | 覆盖旧文件并更新元数据 | 与 vector-store 的 `Write` upsert 语义一致，调用方无需关心重复 |
| 元数据存储 | 每 bucket 一个 JSON 索引文件 | 零依赖，与「本地文件夹模拟对象存储」定位一致 |
| gRPC 端口 | 50054 | 紧接 vector-store 的 50053 顺序分配 |
| 消息大小上限 | 服务端/客户端 `MaxRecvMsgSize` 调至 16MB | gRPC 默认上限 4MB，不足以承载 10MB 文件 |
| bucket 创建 | 首次上传时隐式自动创建 | 避免额外的 CreateBucket 接口，当前无跨业务共享 bucket 的管理需求 |

### Proto 草案

Proto 文件位置：`api/proto/file-store/v1/file_store.proto`（生成代码到 `api/gen/file-store/v1`）。

```protobuf
syntax = "proto3";

package filestore.v1;

option go_package = "github.com/gangantongxue/GanRAG/api/gen/file-store/v1;filestorev1";

// FileStore 对象存储服务
//
// 使用本地文件夹模拟对象存储，提供文件上传、下载、删除、元数据查询与列表能力。
// 文件按 bucket（命名空间）+ key（对象路径）两级定位，同 bucket+key 重复上传覆盖旧文件。
service FileStore {
  // Upload 上传文件（≤10MB），同 bucket+key 已存在时覆盖旧文件并更新元数据
  rpc Upload(UploadRequest) returns (UploadResponse);

  // Download 下载文件内容，文件不存在返回 NotFound
  rpc Download(DownloadRequest) returns (DownloadResponse);

  // Delete 删除文件及其元数据，文件不存在返回 NotFound
  rpc Delete(DeleteRequest) returns (DeleteResponse);

  // GetMetadata 查询单个文件的元数据，文件不存在返回 NotFound
  rpc GetMetadata(GetMetadataRequest) returns (GetMetadataResponse);

  // List 列出 bucket 下所有文件的元数据（bucket 不存在返回空列表）
  rpc List(ListRequest) returns (ListResponse);
}

// FileMetadata 文件元数据
message FileMetadata {
  string bucket = 1;             // 所属命名空间
  string key = 2;                // 对象路径
  int64 size = 3;                // 文件字节数
  string content_type = 4;       // MIME 类型
  string sha256 = 5;             // 内容校验和（十六进制小写）
  int64 uploaded_at_unix = 6;    // 最后上传时间（Unix 秒）
}

// UploadRequest 上传请求（整体携带文件内容）
message UploadRequest {
  string bucket = 1;             // 命名空间（必填），如 attachments / documents
  string key = 2;                // 对象路径（必填），允许 "/" 分层，如 article-123/img.png
  bytes data = 3;                // 文件内容（必填，≤10MB）
  string content_type = 4;       // MIME 类型（可选，缺省 application/octet-stream）
}

message UploadResponse {
  FileMetadata metadata = 1;     // 上传后的最新元数据
}

message DownloadRequest {
  string bucket = 1;
  string key = 2;
}

message DownloadResponse {
  bytes data = 1;                // 文件内容
  FileMetadata metadata = 2;     // 文件元数据（含 sha256，便于调用方校验）
}

message DeleteRequest {
  string bucket = 1;
  string key = 2;
}

message DeleteResponse {}

message GetMetadataRequest {
  string bucket = 1;
  string key = 2;
}

message GetMetadataResponse {
  FileMetadata metadata = 1;
}

message ListRequest {
  string bucket = 1;             // 命名空间（必填）
}

message ListResponse {
  repeated FileMetadata files = 1;
}
```

### 约束与错误码

| 规则 | 说明 |
|------|------|
| key 校验 | 必填、非空；禁止 `..` 路径穿越、禁止以 `/` 开头、禁止包含 `\`；允许 `/` 分层（实现时用 `filepath.Clean` 后二次校验仍在 bucket 根内） |
| bucket 校验 | 必填、仅允许字母数字与 `-` `_` |
| NotFound | Download / Delete / GetMetadata 目标不存在 |
| InvalidArgument | bucket、key 校验失败，或 data 为空 / 超过 10MB |
| ResourceExhausted | 消息体超过服务端接收上限 |

### 元数据存储格式

`data/meta/<bucket>.json`，每个 bucket 一个索引文件，写入时先写临时文件再 `os.Rename` 原子替换，避免进程中断留下损坏索引：

```json
{
  "files": {
    "article-123/img.png": {
      "size": 20480,
      "content_type": "image/png",
      "sha256": "e3b0c442...",
      "uploaded_at_unix": 1759977600
    }
  }
}
```

## 实现状态（2026-10-09 完成）

按本定稿接口实现完毕，使用与验证方式见 [README.md](../README.md)。

| 模块 | 位置 |
|------|------|
| 服务入口 | `service/file-store/cmd/server`（配置加载、日志、优雅退出） |
| 存储核心 | `service/file-store/internal/store`（bucket/key 校验、读写覆盖、JSON 索引原子写、重启加载） |
| gRPC 层 | `service/file-store/internal/server`（5 个 RPC、错误码映射、16MB 消息上限、健康检查与反射） |
| 配置 | `service/file-store/internal/config`（结构体定义与校验，读取经 `pkg/config` 以 viper 加载） |

验证情况：

- 功能测试：`go test -race ./service/file-store/...` 通过（store 单测 + bufconn 端到端，覆盖校验规则、覆盖语义、10MB/16MB 边界、错误码映射、索引重启加载）
- Docker 冒烟：镜像构建、容器启动（50054 端口映射）、5 个 RPC 全链路、容器重启后数据持久化，均已验证通过
