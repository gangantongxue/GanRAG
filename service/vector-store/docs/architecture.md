# Vector Store 服务架构选型

## 概述

自建向量化存储服务，使用 Go 语言内嵌向量存储库，对外暴露 gRPC 接口。

## 技术选型

### 向量存储库

**选型：chromem-go**

- 纯 Go：零依赖，无 CGO
- Chroma 兼容：接口设计与 Python Chroma 一致
- 内存+持久化：开发时内存模式，生产可持久化到文件
- 高性能：精确余弦相似度检索（HNSW 等 ANN 索引在 chromem-go roadmap 中）

**GitHub：** https://github.com/philippgille/chromem-go

### 通信协议

**选型：gRPC**

- 高性能：适合向量数据的批量传输
- 强类型：Protobuf 定义接口
- 流式传输：支持大量向量的流式写入

## 职责

- 向量写入
- 向量检索（近似最近邻）
- 索引管理
- 向量元数据管理

## 接口设计

服务定义：`api/proto/vector-store/v1/vector_store.proto`，生成代码在 `api/gen/vector-store/v1/`（修改 proto 后执行 `scripts/gen-proto.sh`）。

```protobuf
service VectorStore {
  // 集合（索引）管理
  rpc CreateCollection(CreateCollectionRequest) returns (CreateCollectionResponse);
  rpc DeleteCollection(DeleteCollectionRequest) returns (DeleteCollectionResponse);
  rpc ListCollections(ListCollectionsRequest) returns (ListCollectionsResponse);

  // 向量操作
  rpc Write(WriteRequest) returns (WriteResponse);   // unary 批量写入，按 ID upsert
  rpc Search(SearchRequest) returns (SearchResponse); // 调用方传入查询向量
  rpc Delete(DeleteRequest) returns (DeleteResponse); // 按 ID 或元数据/内容条件删除
  rpc GetByID(GetByIDRequest) returns (GetByIDResponse);
}
```

说明：

- 嵌入向量由调用方生成（AI Service 负责向量化），Vector Store 不调用外部 embedding 模型
- 写入与检索均为 unary 批量接口，暂不使用流式
- 同一集合内所有向量维度必须一致，写入与检索时服务端会校验
- 检索为精确余弦相似度，结果按相似度降序排列
- 检索支持元数据精确匹配过滤（`where`）与文档内容过滤（`where_document`，`$contains` / `$not_contains`）

## 服务实现

- 代码结构：
  - `cmd/server`：服务入口（配置加载、日志初始化、优雅退出）
  - `internal/config`：配置结构、校验与类型转换
  - `internal/store`：chromem-go 封装（集合管理、写入、检索、删除）
  - `internal/server`：gRPC handler 与错误码映射、服务生命周期
- 存储模式（`configs/config.yaml` 的 `storage.mode`）：
  - `persistent`（默认）：写入同步落盘，进程重启后自动加载
  - `memory`：内存模式，进程退出后数据丢失，适合开发调试
- 默认监听 `0.0.0.0:50053`，配置项可用 `GANRAG_` 前缀环境变量覆盖
- 已注册 gRPC 健康检查与服务反射（便于 grpcurl 调试）
