# Social Graph 服务架构

> **落地状态：设计定稿，代码未实现**（P3）——实现后更新根 [docs/architecture.md](../../../docs/architecture.md)「设计落地状态」与 [docs/todo.md](../../../docs/todo.md)。
>
> **定位**：为 AI 服务提供「用户 × 文章」的社交关系信号。社交召回总纲见 [docs/social-recall.md](../../../docs/social-recall.md)；AI 消费端见 [service/ai/docs/architecture.md](../../ai/docs/architecture.md)「多路召回与聚合审查」。
>
> **权重纪律**：本服务只产出关系档位与结构信号，不持有融合权重；`w_social` / `w_star` / `w_audit` 等由 AI 服务配置与数据集实验标定。

## 概述

社交图需要常驻内存、周期性跑图算法，并与 AI/Repository 的扩缩容节奏解耦。因此单独建 `service/social-graph/`（即决策中的 `social-graph-service`），通过内部 gRPC 暴露社交信号查询，不内嵌到 AI 服务或 Repository 服务。

- 服务目录：`service/social-graph/`
- 规划端口：`50056`（承接 user 50051、repository 50052、vector-store 50053、ai 50055）
- 通信：内部 gRPC；不直接暴露公网 HTTP
- 存储：**无自有数据库**；图是 User/Repository 业务表的派生只读视图
- 标识：只用 `user_xid` / `kb_xid` / `article_xid`，自增 ID 不出服务

## 职责与非职责

### 职责

- 从 User/Repository 同步关注、KB 成员、star、KB 归属等关系；
- 构建用户社交图，离线计算互关、1~2 跳、PageRank、Leiden、Jaccard 等信号；
- 维护 `UserSocialProfile` 内存快照，提供批量查询；
- 输出关系档位与结构信号，供 AI 服务计算 `socialScore`；
- 暴露重建、健康检查、指标。

### 非职责

- **不做权限判定**：权限过滤由 AI 服务在召回前完成；本服务不接触文章正文，不决定文档可见性；
- **不做最终排序**：融合权重、排序公式由 AI 服务持有；
- **不存业务事实**：关注/star/成员关系的事实来源仍是 User/Repository；本服务只存派生快照；
- **不做文章级 star**：当前 star 是知识库级，文章热度继承所属 KB；
- **不生成聊天文本、不调用 LLM/Decima**。

## 为什么独立服务

1. **生命周期不同**：社交图需常驻内存，内嵌会让每个服务副本各维护一份图，内存成倍浪费且状态不一致。
2. **资源画像不同**：AI 服务计算/GPU 密集，Repository I/O 密集，图计算内存密集 + CPU 中度；混部导致扩缩容错配。
3. **技术栈隔离**：图嵌入未来可能换 Python 生态，独立服务只需改一处。
4. **与 GraphRAG 主流实践一致**：图信号产生与消费解耦。
5. **工程论证**：能讲清「识别异构性并做服务拆分」的取舍。

内嵌仅限早期原型验证；正式开发使用独立服务。

## 技术选型：gograph

**选型：gograph**（module `github.com/FlavioCFOliveira/GoGraph`，MIT）。

| 能力 | 用途 |
|---|---|
| `graph/lpg` | 标签属性图；用户节点、关注/协作/star 边 |
| `graph/adjlist` | 可变邻接表；增量更新与并发快照 |
| `graph/csr` | 不可变 CSR 快照；PageRank/Leiden/BFS 并发读 |
| `graph/query` | 多跳 MATCH 模式查询（可选；MVP 用 BFS） |
| `search` | BFS、Dijkstra、可达集合 |
| `search/centrality` | PageRank / Personalized PageRank |
| `search/community` | Leiden / Label Propagation |
| `examples/11_social_network` | 社交网络完整示例（PageRank + Leiden + friend-of-friend） |

### 风险与约束

- 当前版本为 `v0.17.x`（pre-1.0），API 可能变化；**必须 pin 精确版本**，不跟随 `latest`。
- 生态较小，不能把生产稳定性完全押在库上；用 `internal/graph` adapter 隔离，保留退路：
  - `gonum/graph`（成熟稳定）；
  - 自研 CSR + BFS/PageRank（图规模不大时足够）。
- 边属性优先 `int32`/`int64`，避免 string 属性；用户 key 在内部转成 `int64` 节点 ID，减少内存。
- 官方示例的内存数据只作量级参考，上线前必须用目标图规模本地压测。

### 备选（未选型）

- 轻量社交图：`fluxsocial-go`（若最终只用关注/互关，gograph 显得过重时再评估）。
- Node2Vec / DeepWalk：`graphwizard/embedding` 或 Python 生态；时间允许时作为进阶对比，不在 MVP 引入依赖。

最小使用形态（以选定版本 API 为准）：

```go
g := lpg.New[int64, int64](adjlist.Config{})
g.SetNodeLabel(aliceID, "User")
g.AddEdge(aliceID, bobID, edgeCost)

c := csr.BuildFromAdjList(g.AdjList())
d, _ := search.BFS(c, aliceID)
```

## 图模型

### 节点

- **User**：`user_xid` 内部映射为 `int64` 节点 ID；标签 `User`。
- 不把文章/知识库建成图节点：文章与 KB 是查询上下文，不是图算法对象；避免图规模随内容膨胀。

### 边

| 边 | 方向 | 来源 | 说明 |
|---|---|---|---|
| `FOLLOW` | 有向 | `user_follows` | 我关注作者/库主；边权为关系成本 |
| `MUTUAL_FOLLOW` | 双向检测 | 两条 `FOLLOW` | 不单独存边；建图时检测反向存在，赋更低成本 |
| `CO_MEMBER` | 无向 | `kb_user_relations` | 同一 KB 的成员之间建协作边；可按共同 KB 数加权 |
| `STAR_AUTHOR` | 有向 | `kb_user_relations.starred` + `knowledge_bases.owner_xid` | 我 star 该作者拥有的 KB；按次数加权（弱信号） |

- 无向边（`CO_MEMBER`）在建图时加双向边；Leiden 使用对称投影。`CO_MEMBER` 只服务 PageRank/Leiden/社区等结构算法，直接 `collaborator` 档位由 `member_kbs` 集合索引判断。
- `CO_MEMBER` 边数按 KB 成员数平方增长；成员数超过配置上限的 KB 不建全量协作边（或只连 owner/editor），避免边爆炸。
- `starred_kb` / `own_kb` / `member_kb` 是**用户→KB 的集合索引**，不是用户图边；查询时用 `kb_xid` 直接判断。

### 侧索引

| 索引 | 内容 | 用途 |
|---|---|---|
| `kbOwner[kb_xid]` | `owner_xid` | 判断 `own_kb` / `FOLLOWED_AUTHOR` |
| `userStarredKBs[user_xid]` | `set[kb_xid]` | 判断 `starred_kb` |
| `userMemberKBs[user_xid]` | `set[kb_xid]` | 判断 `collaborator` |
| `starredAuthorCount[user_xid][author_xid]` | count | `starred_author` 弱信号 |

## 数据来源与同步

### 来源

- **User 服务**：`user_follows`（follower_xid、followee_xid）；
- **Repository 服务**：`knowledge_bases`（xid、owner_xid、star_count）、`kb_user_relations`（kb_xid、user_xid、role、starred）。

文章作者与时间不进入图快照：AI 查询时把 `article_xid`、`kb_xid`、`author_xid`、`kb_owner_xid` 作为请求上下文传入；本服务只负责关系信号。

### 同步方式

- **MVP：定时全量导出**。User/Repository 提供内部分页导出 RPC（仅内网，xid，按游标分页），social-graph 周期拉取全量快照。
- **进阶：增量事件**。关注/取关、star/取消 star、成员变更、KB 归属变更时发事件，更新邻接快照；没有事件总线前不做。
- **禁止跨 schema 直连**：不读 User/Repository 的 MySQL 表，遵守服务独立约定。
- 全量导出接口待 P2/P3 补齐，见 [docs/todo.md](../../../docs/todo.md)「User / Repository 社交图数据导出 RPC」。

## 离线计算

```text
拉取全量关系
→ 建图（FOLLOW / CO_MEMBER / STAR_AUTHOR）
→ 互关检测 + 边成本
→ BFS 1~2 跳（按用户，带出度上限）
→ PageRank（关注图）
→ Leiden（无向投影）
→ Jaccard（限 2 跳内）
→ 生成 UserSocialProfile / 信号索引
→ 原子切换内存快照
```

### 计算口径

- **互关**：`FOLLOW(a,b)` 且 `FOLLOW(b,a)`；建图时双向边成本低于单向，具体成本待标定。
- **二跳**：从用户出发的 1~2 跳可达集合；不显式存边。对高出度用户设上限，避免组合爆炸。
- **PageRank**：在关注图上运行；结果归一化后作为所有档位的加成项。
- **Leiden**：在无向投影上运行；每个用户一个社区 ID；同社区只做弱辅助。
- **Jaccard**：关注集合交集/并集；只比较 2 跳内用户，避免全量两两计算。
- **Dijkstra**：进阶项；用互关/单向/协作边成本区分同跳数关系强度。

### 快照结构

```text
UserSocialProfile
  following        set[user_xid]
  followers        set[user_xid]
  mutual           set[user_xid]
  twoHop           set[user_xid]
  community        community_id
  pagerank         float64
  jaccard          map[user_xid]float64
  starredAuthors   map[author_xid]count
  version          string
```

快照不可变；重建时构建新快照，完成后原子替换，查询侧无锁读。

## 在线接口

Proto 规划：`api/proto/social-graph/v1/social_graph.proto`，生成代码在 `api/gen/social-graph/v1/`。

```protobuf
service SocialGraph {
  // 单篇文章的社交信号（AI 已做权限过滤）
  rpc GetArticleSocialSignals(GetArticleSocialSignalsRequest) returns (GetArticleSocialSignalsResponse);

  // 批量版本，供召回候选打分
  rpc BatchGetArticleSocialSignals(BatchGetArticleSocialSignalsRequest) returns (BatchGetArticleSocialSignalsResponse);

  // 调试/离线核对：用户社交画像
  rpc GetUserSocialProfile(GetUserSocialProfileRequest) returns (GetUserSocialProfileResponse);

  // 内部重建与健康检查
  rpc Rebuild(RebuildRequest) returns (RebuildResponse);
  rpc Health(HealthRequest) returns (HealthResponse);
}
```

### `GetArticleSocialSignals`

请求：

```text
user_xid       当前用户
article_xid    候选文章
kb_xid         所属知识库
kb_owner_xid   知识库所有者
author_xid     文章创建者（articles.created_by_xid）
```

响应：

```text
tier                 关系档位（self_authored / own_kb / mutual_follow / follow /
                     collaborator / starred_kb / two_hop / same_community /
                     starred_author / public）
mutual_follow        bool
two_hop              bool
same_community       bool
jaccard              double
pagerank             double   // 已归一化
starred_author_count int32
profile_version      string
```

**边界**：

- 本服务返回**原始关系信号**，不返回最终 `socialScore`；
- 档位到分值的映射、结构加成系数由 AI 服务配置持有；
- 不做权限判断：调用方必须只传已授权候选；
- 请求/响应只用 xid；不返回用户隐私字段。

### 错误与超时

- 统一走 `pkg/errcode`；
- AI 侧设置短超时；超时/不可用按降级处理，不阻塞聊天主链路；
- `Rebuild` 只允许内网/管理调用，幂等。

## 缓存与更新

### 内存快照

- 每个副本常驻一份图与 profile 索引；
- 读多写少：查询走不可变快照，重建走新快照 + 原子切换；
- 多副本各自重建，允许短暂版本不一致；响应带 `profile_version` 便于排查。

### 可选 Redis

MVP 不需要 Redis：查询直接走内存快照。只有在「多副本共享 profile 缓存」有明确收益时再加，key 按项目规范：

```text
social-graph:profile:<user_xid>
```

TTL、刷新策略由配置决定；Redis 不可用时回落到内存快照，不阻塞查询。

### 更新频率

| 数据 | 方式 | 频率 |
|---|---|---|
| 关注/取关、star、成员变更 | 增量（进阶）或全量 | 事件驱动 / 定时 |
| PageRank、Leiden、Jaccard | 全量重建 | 每小时/每天（配置） |
| 二跳可达集合 | 全量重建 | 与图结构同步 |

## 降级与一致性

- 图服务不可用、超时、profile 未命中：AI 侧 `socialScore = 0` 或回退旧五档规则（配置）；
- 降级**不得**改变权限过滤结果，也不得把未授权文档放入候选；
- 旧快照继续服务，直到新快照构建完成；重建失败保留旧快照并告警；
- 直接关系变更后，若增量未启用，允许在重建周期内短暂陈旧；响应 `profile_version` 可用于审计。

## 容量与内存

以下为量级参考，**不是承诺**；上线前用目标图规模本地压测：

| 图规模 | 预估常驻内存 | 说明 |
|---|---|---|
| 1 万节点 / 5 万边 | 数 MB ~ 数十 MB | 无属性边；取决于库实现与索引 |
| 5 万节点 / 25 万边 | 数十 MB | 线性外推 |
| 50 万节点 / 400 万边 | 可能超过数 GB | 官方示例曾出现 8 GB 容器 OOM |

控制手段：

- 边不挂 string 属性，优先 `int32`/`int64`；
- 用户 key 内部转 `int64`，避免重复字符串；
- 只保留算法需要的边；文章/正文不进图；
- 大规模只跑 PageRank 时，评估 gograph 的半外部/CSR 模式。

## 观测与测试

### 指标

- 图规模：节点数、边数、互关边数、平均度；
- 构建：全量同步耗时、建图耗时、PageRank/Leiden/BFS 耗时、快照版本；
- 查询：QPS、P95/P99 延迟、批量大小、未命中率、降级次数；
- 质量：社交分数分布、各档位占比、同社区/Jaccard 命中率。

### 测试

- 确定性小图 fixture：互关、单向、二跳、同社区、Jaccard；
- 边界：空图、孤立节点、自关注、重复边、取消 star；
- 快照切换并发测试（读不阻塞、旧快照可回收）；
- 降级测试：服务不可用/超时，AI 回退不报错；
- 压测：目标规模下的内存与重建耗时；
- gRPC 合约测试：xid 入参、批量、错误码。

## 分期

### MVP

- 关注图 + 互关检测 + BFS 1~2 跳；
- `starred_kb` / `own_kb` / `collaborator` / `starred_author` 索引；
- 批量查询 + 内存快照 + 降级；
- 不启用 PageRank/Leiden/Jaccard。

### 进阶

- PageRank、Leiden、Jaccard、Dijkstra；
- 增量更新（关注/star/成员变更事件）；
- 可选 Redis profile 缓存；
- Node2Vec 对比（时间允许）。

## 配置项

| 配置 | 说明 |
|---|---|
| `rebuild_interval` | 全量重建周期 |
| `bfs_max_hops` | 二跳最大跳数（MVP 1~2） |
| `max_neighbors_per_user` | 高出度用户展开上限，防组合爆炸 |
| `max_members_per_kb_for_comember` | 超过该成员数的 KB 不建全量协作边，防边数平方膨胀 |
| `two_hop_enabled` | 是否计算/返回二跳 |
| `pagerank_enabled` | 是否启用 PageRank |
| `leiden_enabled` | 是否启用社区发现 |
| `jaccard_enabled` | 是否启用共同关注重合度 |
| `edge_cost_mutual` / `edge_cost_follow` / `edge_cost_co_member` | Dijkstra/距离口径的边成本（待标定） |
| `query_timeout` | 查询超时 |

> 融合权重不在本服务配置中；见 AI 服务配置与 [docs/social-recall.md](../../../docs/social-recall.md)「权重纪律」。

## 安全

- 仅内网 gRPC，不暴露公网；如未来需要 HTTP，由 Gateway 代理并鉴权；
- 入参只用 xid，校验 `eid.Valid` / `eid.Match`；
- 不返回用户邮箱等隐私字段；关注/star 关系按产品约定公开可见；
- 查询只接受 AI 已授权候选，服务本身不承担权限判断；
- `Rebuild` 需内网/管理权限，防滥用；
- 日志不打印文章正文、用户敏感信息。

## 风险

1. gograph pre-1.0，API 变动风险 → pin 版本 + adapter + 退路。
2. 全量 BFS 二跳在超大图上的组合爆炸 → 出度上限、活跃用户优先、离线批处理。
3. 多副本快照版本不一致 → 响应版本号 + 容忍短暂陈旧 + 监控。
4. 图数据陈旧 → 重建周期 + 增量事件（进阶）。
5. 内存增长 → 控制边属性、只保留必要边、压测。
6. 误用社交信号做权限 → 明确边界：权限由 AI 前置过滤。
