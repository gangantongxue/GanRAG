# GanRAG 架构选型文档

## 概述

本文档记录 GanRAG 项目的总体架构选型，主要涉及服务间通信、公共基础设施等全局性技术决策。

## 服务架构

```
┌─────────────────────────────────────────────────────────────────┐
│                            Client                               │
└─────────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                       Gateway Service                           │
│                    路由、认证、限流、负载均衡                   │
│                    详见 service/gateway/docs/                   │
└─────────────────────────────────────────────────────────────────┘
                                 │
          ┌──────────────────────┼──────────────────────┐
          ▼                      ▼                      ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│   AI Service    │  │  User Service   │  │    Repository   │
│     (Eino)      │  │                 │  │     Service     │
│    RAG 处理     │  │   用户管理      │  │   知识库管理    │
│   详见 docs     │  │                 │  │                 │
└─────────────────┘  └─────────────────┘  └─────────────────┘
          │                                       │
          │         基础设施服务（gRPC）          │
          ├───────────────────┬───────────────────┘
          ▼                   ▼
┌─────────────────┐  ┌─────────────────┐
│  Vector Store   │  │   File Store    │
│   向量化存储    │  │    对象存储     │
│  详见 docs      │  │   详见 docs     │
└─────────────────┘  └─────────────────┘
```

## 技术选型

### 服务间通信

**选型：gRPC**

- 高性能：基于 HTTP/2，支持多路复用
- 强类型：通过 Protobuf 定义接口，编译时类型检查
- 跨语言：支持多种编程语言
- 生态丰富：支持拦截器、负载均衡、服务发现等

### 数据库

**选型：MySQL**

- 成熟稳定：广泛使用，社区活跃
- 功能完善：支持事务、索引、外键等
- 性能优异：适合读写混合场景

**ORM：GORM**

- 功能完善：关联、事务、钩子等
- 代码生成：配合 Generator 生成模型代码
- 扩展性强：支持自定义数据类型、插件

**迁移工具：golang-migrate**

- SQL 支持：可直接从 SQL 语句导入
- 成对迁移：每个迁移包含 up.sql 和 down.sql
- 版本控制：支持版本号管理和回滚

### 缓存

**选型：Redis**

- 高性能：内存数据库，读写速度快
- 数据结构丰富：支持 String、Hash、List、Set 等
- 持久化：支持 RDB 和 AOF

### 日志

**选型：slog + lumberjack**

Go 标准库结构化日志 + 日志文件轮转：

- slog：Go 1.21+ 标准库，零依赖
- lumberjack：日志文件切割、轮转、压缩

**日志输出：**
- 控制台：便于开发调试
- 文件：便于生产环境日志收集

### 配置管理

**YAML 配置：zap**

- 结构化配置：支持嵌套结构
- 类型安全：编译时检查
- 环境覆盖：支持环境变量覆盖

**环境变量：GoDotEnv**

- 标准化：遵循 .env 文件标准
- 简单易用：一行代码加载
- 安全性：敏感信息不入代码库

## 技术栈

### 前端

| 层级 | 选型 | 说明 |
|------|------|------|
| 框架 | React + TypeScript | UI 组件化、类型安全 |
| AI 聊天 | Vercel AI SDK | 流式聊天、状态管理 |
| Markdown | streamdown | 流式渲染、代码高亮 |
| 构建工具 | Vite | 快速、轻量 |
| 状态管理 | Zustand | 轻量级 |
| UI 组件库 | Shadcn/ui + Aceternity UI | 基础组件 + 炫酷效果 |
| 样式方案 | Tailwind CSS | 原子化 CSS |
| 动画引擎 | Framer Motion | 流畅动画 |
| HTTP 请求 | Axios | 通用请求 |
| 路由 | React Router | 官方路由 |
| 表单 | React Hook Form | 高性能表单 |

### 后端

**Go 版本：1.27**

| 层级 | 选型 | 说明 |
|------|------|------|
| HTTP 框架 | Hertz | Gateway 使用，详见 service/gateway/docs/ |
| AI 框架 | Eino | AI Service 使用，详见 service/ai/docs/ |
| 服务间通信 | gRPC | 内部服务通信 |
| 数据库 | MySQL | 主数据存储 |
| ORM | GORM | 数据库访问 + Generator 代码生成 |
| 迁移工具 | golang-migrate | SQL 导入，up/down 成对 |
| 缓存 | Redis | 高速缓存 |
| 日志 | slog + lumberjack | 标准库结构化日志 + 文件轮转 |
| 配置(YAML) | zap | 结构化配置 |
| 配置(.env) | GoDotEnv | 环境变量 |
| 向量存储 | chromem-go | 纯 Go 内嵌向量库，详见 service/vector-store/docs/ |
| 对象存储 | 自建 (os 包) | Go 标准库实现，详见 service/file-store/docs/ |

## 各服务架构详情

| 服务 | 架构文档 |
|------|----------|
| Gateway | [service/gateway/docs/architecture.md](../../service/gateway/docs/architecture.md) |
| AI Service | [service/ai/docs/architecture.md](../../service/ai/docs/architecture.md) |
| User Service | - |
| Repository Service | - |
| Vector Store | [service/vector-store/docs/architecture.md](../../service/vector-store/docs/architecture.md) |
| File Store | [service/file-store/docs/architecture.md](../../service/file-store/docs/architecture.md) |

## 决策记录

| 日期 | 决策 | 原因 |
|------|------|------|
| 2026-09-12 | 采用 gRPC 做服务间通信 | 高性能、强类型、跨语言 |
| 2026-09-12 | 采用 MySQL 作为主数据库 | 成熟稳定、功能完善 |
| 2026-09-12 | 采用 Redis 作为缓存 | 高性能、数据结构丰富 |
| 2026-09-12 | 采用 zap 读取 YAML 配置 | 结构化、类型安全 |
| 2026-09-12 | 采用 GoDotEnv 读取 .env | 标准化、安全 |
| 2026-09-12 | 采用 slog + lumberjack 作为日志方案 | 标准库、文件轮转 |
| 2026-09-12 | 采用 GORM 作为 ORM | 功能完善、代码生成 |
| 2026-09-12 | 采用 golang-migrate 做数据库迁移 | SQL 导入、up/down 成对 |
| 2026-09-12 | 自建 Vector Store 服务 | Go 内嵌向量库 + gRPC |
| 2026-09-12 | 自建 File Store 服务 | 本地文件模拟 + gRPC |
