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

## 接口设计（待定）

```protobuf
service FileStore {
  rpc Upload(stream UploadRequest) returns (UploadResponse);
  rpc Download(DownloadRequest) returns (stream DownloadResponse);
  rpc Delete(DeleteRequest) returns (DeleteResponse);
  rpc GetMetadata(GetMetadataRequest) returns (GetMetadataResponse);
}
```
