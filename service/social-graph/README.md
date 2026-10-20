# Social Graph 服务

为 AI 服务提供「用户 × 文章」的社交关系信号：关注/互关、KB 成员协作、star 作者、二跳、PageRank、Leiden、Jaccard 等。

详细设计见 [docs/architecture.md](docs/architecture.md)；召回总纲见 [docs/social-recall.md](../../docs/social-recall.md)。

> 当前状态：**仅设计文档，代码未实现**（P3）。实现时补 `cmd/server`、`internal/`、`Taskfile.yml`、`configs/` 与 `scripts/`，端口规划 `50056`。

## 边界

- 无自有数据库：图由 User/Repository 现有表派生；
- 只产出关系档位/结构信号，不做权限判定、不做最终排序；
- 通过内部 gRPC 提供服务，不直接暴露公网 HTTP。

## 相关文档

- [社交召回与权重标定](../../docs/social-recall.md)
- [AI 服务多路召回与聚合审查](../ai/docs/architecture.md)
- [待完成事项](../../docs/todo.md)
