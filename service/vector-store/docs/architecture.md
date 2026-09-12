# Vector Store 服务架构选型

## 概述

自建向量化存储服务，使用 Go 语言内嵌向量存储库，对外暴露 gRPC 接口。

## 技术选型

### 向量存储库

**选型：chromem-go**

- 纯 Go：零依赖，无 CGO
- Chroma 兼容：接口设计与 Python Chroma 一致
- 内存+持久化：开发时内存模式，生产可持久化到文件
- 高性能：HNSW 算法，支持近似最近邻搜索

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

## 接口设计（待定）

```protobuf
service VectorStore {
  rpc Write(WriteRequest) returns (WriteResponse);
  rpc Search(SearchRequest) returns (SearchResponse);
  rpc Delete(DeleteRequest) returns (DeleteResponse);
}
```
