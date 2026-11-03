# 社交关系感知召回与权重标定

> **定位**：P3 社交召回的设计与调优总纲。服务实现见 [service/social-graph/docs/architecture.md](../service/social-graph/docs/architecture.md)，消费端接入见 [service/ai/docs/architecture.md](../service/ai/docs/architecture.md)「多路召回与聚合排序」。
>
> **权重纪律**：本文只给**相对优先级与调优方法**，不写具体权重数值。融合权重、关系档位分差、时效半衰期、PageRank/Jaccard/社区加成系数、RRF 的 `k` 等均为**待标定超参**，由公开数据集实验确定，上线后再用真实日志与 A/B 测试微调；归一化封顶、衰减下限、`log1p` 压缩、排除自身 star、`1/(1+distance)` 等为**结构性常数**，不参与权重调优。
>
> **模型分工**：Decima-small 只负责 Agent 选择；Qwen3-Reranker-0.6B 只负责语义精排；Qwen3.5-4B 只负责离线调参评分。最终排序由融合公式决定，任何语义重排不得覆盖社交权重。

## 1. 目标与边界

### 1.1 现状

初始召回采用硬编码层级：

```text
自己的文章 > 协作者文章 > 自己 star 的知识库文章 > 自己关注的人的文章 > 公开文章
```

问题：

- 只表达「有没有关系」，不表达「关系有多近」；
- 互关、二跳关注、共同关注重合、社区归属、作者影响力等信号无法进入排序；
- 权重写死在代码/文档里，无法解释、消融和复现；
- star 与时效只有粗略规则，缺少归一化与标定口径。

### 1.2 目标

- 把社交关系网分数化：直接关系事实 + 结构派生算法 + 可选嵌入；
- 保留知识库 star 热度与文章时效两个信号；
- 语义相关性由专用重排模型增强，但最终排序由融合公式决定；
- 召回排序可解释、可配置、可消融、可降级；
- 用分部数据集基线 + 两层统一测试集完成第一轮权重标定，真实日志/A/B 测试做最终微调。

### 1.3 不做什么

- **不引入文章级 star**：当前产品的 star 语义是「star 知识库」，文章热度继承所属知识库；如未来产品确需文章级 star，另立迁移与接口，不在本次范围。
- **不新增 follow-kb 表**：关注知识库就是 star 该库；关注用户仍走 `user_follows`。
- **不让社交信号绕过权限**：权限硬过滤永远先于任何召回与排序。
- **不在 P3 上线 Node2Vec/GNN**：作为时间允许时的进阶对比项。
- **不把融合数据集调出的权重当生产最优值**：它只是合理初值。
- **不调整 Agent 选择模型与提示词**：R3-Skill / AgentSelect 后置，不纳入召回权重调优链路。
- **不采用 Qwen3-Reranker-4B**：4B 在 8GB 显存下不适合在线；采用 Qwen3-Reranker-0.6B（与现有 Qwen3-Embedding-0.6B 同族）。
- **不允许最终融合后全局重排**：Qwen3-Reranker-0.6B 只作用于语义召回之后、时效/star/社交融合之前。
- **不把 Qwen3.5-4B 放进在线链路**：它只做离线调参评分，不输出参数值。

## 2. 与现有模型对齐

| 决策中的概念 | GanRAG 现有模型 | 统一口径 |
|---|---|---|
| 文章 star 数 | `knowledge_bases.star_count`、`kb_user_relations.starred`；无文章级 star 表 | **知识库级 star**；文章热度取所属知识库 star 数 |
| 自己的文章 | `articles.created_by_xid` | `self_authored = article.created_by_xid == me` |
| 自己的知识库 | `knowledge_bases.owner_xid` | `own_kb = kb.owner_xid == me` |
| 协作者 | `kb_user_relations.role >= 1`（reader/editor） | **KB 成员即协作者**；文章没有共同作者表 |
| 关注知识库 | 无 follow-kb 表 | **关注知识库 = star 该库**，不产生额外关系行 |
| 关注作者 | `user_follows` | `follow = user_follows(me, 作者/库主)`；与是否 star 其库无关 |
| 互相关注 | 两条有向 `user_follows` | 计算时检测双向存在，不单独存边 |
| 二跳关注 | 无显式边 | BFS 离线计算可达集合，不存边；含「关注的人的关注」与「关注我的人关注的人」 |
| 时效基准时间 | `articles.updated_at`（最后修改时间）；无最后修改时间时用 `articles.created_at` 兜底 | 时效衰减的**唯一**时间基准；刚创建的文章两值相同，兜底天然成立 |
| 用户/知识库/文章标识 | 对外统一 xid | 社交图服务只用 `user_xid` / `kb_xid` / `article_xid`，自增 ID 不出服务 |
| 关注库主 | `user_follows` 可独立存在 | 只有显式关注用户时才算 `FOLLOWED_AUTHOR`，不由 star 推导 |

**关键区分**：

- `STARRED_KB`：我 star 了这篇文章所属的知识库，是**直接关系**；
- `STARRED_AUTHOR`：我 star 过该作者拥有的其他知识库，是**弱作者聚合信号**；
- `FOLLOWED_AUTHOR`：我显式关注了该作者/库主，是**用户关注关系**，与 star 是两件事。

> 最新通用方案中的「文章 star / 文章协作者」在本项目分别映射为「知识库 star（文章继承）」与「KB 成员即协作者」；本文后续统一使用项目口径。

## 3. 模型分工与召回流程

### 3.1 模型角色表

| 模型 | 所处环节 | 输入 | 输出 | 主要用途 | 是否在线 |
|---|---|---|---|---|---|
| **Decima-small** | 多智能体路由 | 用户问题、候选 Agent | `choice` / `score` / 概率 | Agent 选择与切换 | 是 |
| **Qwen3-Reranker-0.6B** | 语义召回之后、最终融合之前 | query + 语义候选文章 | `rerankScore` | 语义相关性精排；可离线质检 | 可选；可先离线 |
| **Qwen3.5-4B** | 离线自动调参 | 当前参数下的最终融合排序、`user_context`、固定 rubric | 排序质量评分 | 为优化器提供反馈 | 否，仅离线 |

**结论：**

- 生产 Agent 选择仍由 **Decima-small** 负责；召回链路的语义精排改由 **Qwen3-Reranker-0.6B** 承担；
- 最终排序决策由**融合公式和权重参数**决定，不是 Qwen3-Reranker-0.6B；
- Qwen3-Reranker-0.6B 只改善「语义相关性」这一路的判别质量；
- Qwen3.5-4B 只评估最终融合排序质量，不进入在线链路，不直接输出参数数值。

### 3.2 处理顺序

```text
权限硬过滤
→ 向量召回 Top-Nv + 关键词召回 Top-Nk
→ 合并去重 → 语义路融合（排名归一化或 RRF）
→ 【可选】Qwen3-Reranker-0.6B → rerankScore → 排名化/分位数化
→ semanticScore
→ base = semanticScore × timeDecay
→ final = base + w_star × starScore + w_social × socialScore
→ 取 Top-K → 最终排序
```

权限过滤必须先于两条召回链路：私有库排除非成员，公开库允许所有人；未授权文档不得进入候选集，也不得发送给 Qwen3-Reranker。

### 3.3 公式（符号化）

```text
R(d)      = Fuse(V(d), K(d), [N(d)])          // 语义路；N(d) 为可选 Decima noul 排名
Q(d)      = Qwen3Reranker(query, d)           // 可选语义精排
semanticScore(d) = Q(d) 经候选集内排名化/分位数化
                 = 或 R(d)（未启用精排时）
base(d)   = semanticScore(d) × timeDecay(d)
final(d)  = base(d)
          + w_star   × starScore(kb(d))
          + w_social × socialScore(u, d)
```

其中：

- `V(d)` / `K(d)`：向量、关键词两路在该 query 候选集内的排名归一化分数；未命中的链路记 0；
- `Fuse` 可选线性融合或 RRF：
  - 线性：`R = w_v × V + w_k × K + w_both × A`，`A` 为双路命中标记；
  - RRF：`R = Σ 1/(k + rank_i(d))`，对量纲不敏感，适合社交分分布不均时兜底；
- `N(d)`：可选的 Decima `noul` 语义排名，默认关闭；若启用，只作为语义路第三路排序信号，不进入最终加性融合；
- `Q(d)`：Qwen3-Reranker-0.6B 的语义相关性分数；原始分不校准，必须先做候选集内排名化/分位数化；
- `timeDecay`：按「最后修改时间（`updated_at`，无则 `created_at` 兜底）」计算，带结构性下限，防止经典老文章被完全压死；**时效是弱修正信号**——对排序的影响应显著小于语义与社交分，标定时半衰期以长档为主、衰减下限取较高水平，时间只做温和压制、不主导排序；
- `starScore`：知识库 star 数的分位数归一化 + `log1p` 压缩 + P95 封顶；
- `socialScore`：用户 × 文章的社交分数，完全由社交图服务离线算好、在线查表；
- **不再有 `w_audit`**：原 Decima 后置审查项已退出主公式；语义相关性只通过 `semanticScore` 进入排序。

### 3.4 为什么时效用乘性、star/社交用加性

- **时效乘性**：过时文章语义再相关也应被压降，而不是被高语义分抵消；这是结构性选择；
- **star/社交加性**：二者可相互补偿——社交远但 star 极高仍值得推，社交近但 star 一般也应有机会；
- **语义精排只做语义分**：它不感知社交图，不能作为最终排序的裁决者；只增强 `semanticScore`，再由融合公式决定最终顺序。

### 3.5 语义精排插入点与硬约束

**硬约束：**

1. Qwen3-Reranker-0.6B 位于**语义召回之后、时间衰减与社交/star 加性融合之前**；
2. 它的输出只作为 `semanticScore` 的增强或替代，不直接输出最终排序；
3. **禁止**在 `final = base + w_star·starScore + w_social·socialScore` 之后再做全局 rerank 并直接取 Top-K；
4. `rerankScore` 校准不佳，不能直接与社交分、star 分做原始数值加性融合；必须先做候选集内排名化或分位数化；
5. 任何语义重排序不得绕过或覆盖 `w_social`、`w_star`、`timeDecay` 的最终融合排序；社交关系权重是毕设主线，重排序只服务语义相关性判别；
6. Qwen3-Reranker-0.6B 的分数**不直接驱动参数决策**；
7. Decima `noul` 若保留，只能作为**语义路可选排序器**（默认关闭），不得放到最终融合之后；
8. 精排模型不可用、超时或未启用时，回落到语义路融合结果，不阻塞聊天主链路。

**选型说明：**

- 采用 `Qwen3-Reranker-0.6B`：FP16 权重约 1.2GB，8GB 显存可在线的同时保留余量；与现有 `Qwen3-Embedding-0.6B` 同族，tokenizer/prompt 风格一致；
- 不采用 `Qwen3-Reranker-4B`：BF16 权重约 8GB，开发机 RTX 4060 Laptop 8GB 无法稳定在线；如未来换更大显存，再评估 4B 或 8B；
- 备选对比 `bge-reranker-v2-m3`（568M）只在 0.6B 在中文难例上明显不达标时评估，不提前引入第二套精排服务；
- 推理服务：Qwen3-Reranker 需要独立的重排推理服务（Transformers/vLLM/llama.cpp 选型在实现时评估），不与现有 Ollama 向量接口混用；离线质检可先用 Python 脚本；
- 精排候选规模 `Top-N` 与超时是工程配置，不是权重；上线前必须实测 P95 延迟与吞吐；
- 离线质检与在线精排共用同一模型与排名化逻辑，避免两套口径。

### 3.6 候选元数据契约

AI 服务在向量写入/检索时需要拿到以下字段（不可变字段可随向量 metadata 写入，动态字段查询时从 Repository 或缓存取）：

| 字段 | 来源 | 用途 |
|---|---|---|
| `article_xid` / `kb_xid` | 向量 metadata | 候选标识、权限过滤 |
| `author_xid` | `articles.created_by_xid` | 作者关系、社交分 |
| `kb_owner_xid` | `knowledge_bases.owner_xid` | 库主关系、社交分 |
| `articles.updated_at`（无则 `created_at`） | `articles` | 时效衰减 |
| `kb_star_count` | `knowledge_bases.star_count` | star 热度（动态，查询时取最新值） |

运行时另保留 `vector_rank`、`keyword_rank`、`rerank_score`、`semantic_score`、`time_decay`、`star_score`、`social_score` 与 `final_score` 分项，用于解释、调参与消融；这些是运行时字段，不写入向量 metadata。

### 3.7 融合方式与消融备选

主公式固定为「时效乘性 + star/社交加性」。以下三种只作为消融对比，不替换主公式：

1. **加权线性融合（基线）**：`score = α × semanticScore + (1-α) × socialScore`，`α` 待标定；用于回答「语义 vs 社交的粗比例」，是消融用简化式，完整排序仍含时效与 star；
2. **RRF**：`score = Σ 1/(k + rank_i)`，对量纲不敏感；用于社交分分布不均时的稳健性对比；
3. **Rerank 特征**：语义取 Top-N 后，把社交分仅作为 rerank 特征，避免污染粗召回。

无论哪种方式，都不得在最终融合后引入一个全局语义重排并直接取 Top-K。

## 4. 关系建模

### 4.1 三层模型

1. **直接关系事实层**：`self_authored`、`own_kb`、`mutual_follow`、`follow`、`collaborator`、`starred_kb`、`starred_author`、`public`。
2. **结构派生算法层**：BFS 1~2 跳、PageRank、Leiden 社区、Jaccard 共同关注重合度、Dijkstra 加权最短路（进阶）。
3. **嵌入学习层（可选）**：Node2Vec 社交嵌入，作为综合结构邻近性信号，与图分数**并列比较**，不简单叠加。

### 4.2 直接关系

| 信号 | 数据来源 | 语义 |
|---|---|---|
| `self_authored` | `articles.created_by_xid = me` | 我创建的文章 |
| `own_kb` | `knowledge_bases.owner_xid = me` | 我拥有的知识库中的文章 |
| `mutual_follow` | `user_follows` 双向存在 | 互相关注 |
| `follow` | `user_follows(me, author/owner)` | 我单向关注作者/库主 |
| `collaborator` | `kb_user_relations.role >= 1` | 我是该 KB 成员（reader/editor） |
| `starred_kb` | `kb_user_relations.starred = 1` | 我 star 了该 KB（即关注该库） |
| `starred_author` | 统计我 star 过该作者拥有的 KB 数 | 弱兴趣信号，按作者聚合 |
| `public` | 以上皆无，且 `visibility = 1` | 公开库兜底 |

**说明**：

- `mutual_follow` 不单独存边：建图时保留两条有向 `follow` 边，计算时检测双向；Dijkstra 边权（互关成本 < 单向成本）是待标定超参，二跳由 BFS 单独计算；
- `collaborator` 由 KB 成员关系派生，不依赖文章共同作者表；
- `starred_author` 是用户→作者的弱信号，不建用户-用户边；在文章中通过 `author_xid` 查询，计数先做分档/归一化再映射为档位分，避免无上限放大；
- `starred_kb` 与 `follow` 独立：star 库不等于关注库主；
- 最新通用方案中的 `followee_followee` 与 `follower_follower` 都归入 `two_hop`：前者是「关注的人的关注」，后者是「关注我的人关注的人」，由 BFS 离线计算，不显式存边。

### 4.3 结构派生

| 算法 | 用途 | 计算时机 |
|---|---|---|
| BFS 1~2 跳 | 二跳关注可达集合（关注的人的关注、关注我的人关注的人） | 离线全量/定时 |
| PageRank | 用户全局影响力，作为所有档位的加成项 | 离线全量/定时 |
| Leiden | 社区归属，同社区弱加成 | 离线全量/定时 |
| Jaccard | 共同关注重合度（限 2 跳内） | 离线全量/定时 |
| Dijkstra | 同跳数但关系强度不同时的加权距离（进阶） | 离线全量/定时 |

- 二跳**不显式存边**，避免边数爆炸；离线算好 `map[user]reachable`；
- PageRank 作用在关注/协作等用户图上，作为**加成项**，不替代直接关系档位；
- Leiden 在无向投影上运行；同社区只做弱辅助信号；
- Jaccard 只比较 2 跳内用户，避免全量两两计算。

### 4.4 社交分数

```text
tier         = max(kbTier(kb, kb_owner), authorTier(article_author))
socialScore  = clamp(tierScore(tier)
                   + pagerankBonus
                   + communityBonus
                   + jaccardBonus, 0, 1)
```

- `kbTier` 覆盖 `own_kb` / `collaborator` / `starred_kb` / `follow(owner)` 等 KB 级关系；`authorTier` 覆盖 `self_authored` / `mutual_follow` / `follow` / `starred_author` 等作者级关系；social-graph 服务最终返回这个 `tier`；
- `tier` 是直接关系档位；多信号命中时**取最高档**，不把档位分值相加，避免权重爆炸；
- `pagerankBonus`、`communityBonus`、`jaccardBonus` 是结构派生加成，系数待标定；
- 没有直接关系时，`tierScore` 依次回落到 `two_hop` / `same_community` / `jaccard` / `starred_author` / `public` 兜底档；结构加成可在此基础上抬升；
- 若 `tier` 已经是 `same_community` / `jaccard`，不再重复加对应 bonus；更高档位是否叠加同社区/Jaccard 由消融决定；
- Node2Vec 若启用，作为 `socialScore` 的**替代或并列对比**，不与上述加成简单相加；
- `1/(1+distance)` 是把距离转分数的结构性公式，不参与权重调优。

### 4.5 关系优先级（只给序关系）

```text
self_authored / own_kb
  > mutual_follow
  > follow
  > collaborator
  > starred_kb
  > two_hop
  > same_community / jaccard
  > starred_author
  > public
```

- 上表只保证排序，不保证相邻档位等距；实际分差由数据集实验反推；
- `self_authored` 与 `own_kb` 默认同属最高档；如产品需要，可在档位内再细分；
- `PageRank` 是所有档位之上的加成项，不是独立档位；
- `starred_author` 是弱作者聚合信号，低于同社区/Jaccard 等结构信号；
- 最新通用方案中的「star 过该作者的文章」对应本项目的 `starred_author`；`starred_kb` 是更强的直接关系，位置高于 `two_hop`。

## 5. 社交图服务

### 5.1 为什么独立

推荐独立 `service/social-graph/`（即决策中的 `social-graph-service`），不内嵌到 AI/Repository：

1. 社交图需常驻内存，内嵌会让每个服务副本各维护一份图，内存成倍浪费且状态不一致；
2. AI 服务计算/GPU 密集，Repository I/O 密集，图计算内存密集 + CPU 中度，混部导致扩缩容错配；
3. 图嵌入未来可能换 Python 生态，独立服务只需改一处；
4. 与 GraphRAG 主流实践一致：图信号产生与消费解耦；
5. 工程判断可作为答辩加分项。

内嵌仅限早期原型验证；正式开发用独立服务。

### 5.2 数据流

```text
User 服务（user_follows）
Repository 服务（knowledge_bases / kb_user_relations；文章作者/时间由 AI 查询上下文带入）
        │ 内部分页导出 RPC（xid，不跨库直连）
        ▼
social-graph 服务
  建图 → BFS / PageRank / Leiden / Jaccard → UserSocialProfile
        │ 内存快照 + gRPC 查询
        ▼
AI 服务：权限过滤后的候选 + 社交分 → 语义精排 → 最终融合排序
```

- 社交图服务**无自有数据库**，图是现有业务表的派生只读视图；
- 不跨 schema JOIN；通过 User/Repository 的内部导出 RPC 拉取，接口待 P2/P3 补齐；
- 图更新：定时全量重建；直接关系（新关注/取关/star/成员变更）后续可做增量，但必须保持快照可切换。

### 5.3 降级

- 图服务不可用或 profile 未命中：`socialScore = 0`，或回退旧五档规则（可配置）；
- 降级不得改变权限过滤结果；
- 降级、缓存命中率、重建耗时、图规模需要指标观测。

## 6. 权重纪律

### 6.1 待标定超参

| 超参 | 说明 | 标定来源 |
|---|---|---|
| `w_social` | 社交分数融合权重 | 分部一阶社交标定 + 两层统一测试集联合校准 |
| `w_star` | star 热度融合权重 | 同上 |
| `w_v` / `w_k` / `w_both` 或 `α` | 语义、关键词、双路命中的融合比例 | 语义基线与联合校准 |
| 关系档位分差 | 互关、单向、协作者、starred_kb、二跳等实际分差 | 一阶社交数据 + 合成社交层变体 |
| `timeDecay` 半衰期/衰减函数 | 时效衰减速度 | ChronoQA 单独校准 + 联合校准；**弱影响约束**：影响显著小于语义/社交，半衰期以长档为主 |
| PageRank 加成系数 | 影响力加成强度 | 合成社交层消融 |
| 社区/Jaccard 系数 | 结构辅助信号强度 | 合成社交层消融 |
| `RRF` 的 `k` | RRF 平滑参数 | 第一层扫描或固定常用值 |
| 二跳是否纳入 | 二跳是否有正贡献 | 合成社交层消融 |
| Node2Vec 是否启用 | 嵌入是否优于纯图分数 | 时间允许时对比 |

### 6.2 结构性常数

不参与权重调优：

- 权限硬过滤顺序；
- P95 分位数封顶的定义（具体 P95 值由数据分布统计，定期刷新，不是权重）；
- `log1p` 压缩；
- 衰减下限；
- 排除自身 star（当前用户 star 过该 KB 时，全局 star 数减 1，避免重复奖励）；
- `1/(1+distance)` 距离转分数；
- 分数 clamp 到 `[0,1]`；
- 排名归一化公式、RRF 公式本身；
- 语义精排分数的候选集内排名化/分位数化；
- Top-Nv / Top-Nk / Top-K / 重排 Top-N 等候选规模（工程配置，但不是权重）。

### 6.3 工程配置与选型

| 配置/选型 | 说明 | 默认 |
|---|---|---|
| `rerank_enabled` | 是否启用语义精排 | 先离线质检；在线开关按延迟评估 |
| `rerank_model` | 精排模型 | `Qwen3-Reranker-0.6B` |
| `rerank_top_n` | 精排候选规模 | 工程配置，按延迟与效果扫描 |
| `rerank_timeout` | 精排超时 | 短超时，失败回落 |
| `semantic_norm_mode` | 排名化/分位数化 | 结构性处理，不调参 |
| `decima_noul_fallback_enabled` | Decima `noul` 是否作为语义路可选排序器 | `false`；仅降级/消融 |
| `social_fallback_mode` | `zero` 或 `legacy_tier` | 工程配置 |

### 6.4 配置归属

- 权重与阈值由 **AI 服务配置**持有，不存数据库；
- 社交图服务只产出分数/画像，不决定最终排序；
- 每次实验记录配置版本、数据集版本、切分方式与指标，保证可复现。

## 7. 公开数据集选型

### 7.1 核心边界

公开数据集中**没有现成的、直接带有「用户社交关系 + 知识库文章 + 相关性标注」三元组的中文基准数据集**。学术基准通常不覆盖这种细粒度社交上下文。

公开社交推荐数据集（DISGCN、Dianping、Douban 等）在结构上**只能原生支持一阶关系**，无法直接提供二跳、PageRank、Leiden 社区、Jaccard、Node2Vec 等复杂图特征的相关性标注。因此区分：

- **一阶社交关系 + star/交互信号**：用公开社交推荐数据集做有监督校准；
- **复杂图特征**：通过「在语义基准上合成社交层 + 消融扫描」确定边际贡献与安全取值区间。

### 7.2 数据集角色表

| 数据集 | 主要用途 | 优先级 | 使用方式 |
|---|---|---|---|
| **T2Ranking** | 底层 query-文章相关性排序基线；训练 LambdaRank | 最高 | 使用四级相关性 qrels，将向量相似度、关键词匹配分数作为特征训练排序模型；30 万+ 查询、200 万+ 段落 |
| **DISGCN** | 模拟「用户—一阶关注关系—文章」社交特征，调一阶社交权重与 star/交互信号 | 高 | 物品映射为文章，好友关系映射为关注关系，交互作为隐式相关性信号；受控注入多种社交密度；构造「单向边标记为互关」变体，反推互关/单向分差 |
| **Dianping_SocialRec** | 补充一阶社交校准与泛化验证 | 高 | 约 63 万条无向社交边、215 万条评分；验证一阶社交权重在另一中文社交推荐分布上的稳定性 |
| **Douban** | 中文社区关系密度与内容交互模式参照；补充验证 | 中 | 校验一阶权重合理性，为合成图社交密度设定提供参照；不主导调参 |
| **ChronoQA** | 单独校准时效性权重 | 高 | 固定其他特征，仅扫描时间衰减函数参数；5,176 个中文问题，覆盖绝对、聚合、相对时间类型 |
| **DuReader_retrieval** | 补充测试集，验证语义/字面排序权重泛化性 | 中 | 作为 T2Ranking 的补充测试集，不主导调参 |
| **ZhihuRec** | 中文内容社区「用户—作者—回答」排序规律补充校验 | 中 | 轻量补充验证；不纳入核心调优链路 |
| **R3-Skill / AgentSelect** | Agent 选择模型微调或路由评估 | 低 | 放在召回权重调优之后进行，不作为当前重点 |

**核心主线为：T2Ranking + DISGCN/Dianping + ChronoQA。**

- T2Ranking：语义/字面相关性基线；
- DISGCN/Dianping：一阶社交关系与 star/交互信号的融合权重；
- ChronoQA：发布时间与最后修改时间的时效性衰减参数。

Douban、DuReader、ZhihuRec 做补充校验；R3-Skill / AgentSelect 后置。

> 数据集名称、角色与两层方法属于稳定设计信息，记录于此；下载地址、原始字段、融合脚本、缓存、抽查运行细节收敛在 `datasets/README.md`（整目录不入 git）。

### 7.3 各数据集能调什么、不能调什么

| 调优对象 | 可用数据集/方法 | 说明 |
|---|---|---|
| 语义与关键词排序基线 | T2Ranking、DuReader_retrieval | 有监督学习 LambdaRank；四级相关性标签 |
| `w_social` 一阶基线量级 | DISGCN、Dianping | 在一阶关注/互关与交互信号下做联合校准 |
| `w_star` 与社交相对关系 | DISGCN、Dianping | star/交互作为隐式质量信号；可排除自身交互的重复奖励 |
| 互关 vs 单向关注分差 | DISGCN 构造变体 | 将部分单向边标记为互关，观察 NDCG 变化反推分差量级 |
| 协作者信号 | DISGCN/Dianping 共同交互近似 | 以「共同交互过同一物品」近似「合作过同一文章」 |
| 时效半衰期 | ChronoQA | 单独扫描指数/线性衰减参数，观察时间窗口命中与端到端时序准确率 |
| 二跳是否纳入及分差 | T2Ranking 上合成社交层 + 消融 | 公开数据无原生二跳标注；扫描二跳加成系数，观察 NDCG 方向与敏感区间 |
| PageRank 加成系数 | 合成社交层 + 消融 | 扫描加成系数；确定稳健区间，不追求绝对权重 |
| Leiden 同社区贡献 | 合成社交层 + 消融 | 观察同社区信号是否有正贡献；决定是否纳入进阶阶段 |
| Jaccard 共同关注重合度 | 合成社交层 + 消融 | 观察边际贡献与敏感区间 |
| RRF 的 k | 第一层合并测试集扫描或固定常用值 | k 可作结构性常数或超参扫描 |
| Node2Vec 是否启用 | 时间允许时对比 | 对比嵌入信号是否优于纯图分数；不阻塞主线 |
| 语义精排是否启用/选型 | T2Ranking 候选集对比 | 无重排 / Decima noul / Qwen3-Reranker-0.6B（可选 bge-reranker-v2-m3）；看 NDCG@10 与 P95 延迟 |
| P95 归一化封顶 | 数据分布统计 | 统计参数，不是权重；需随数据定期刷新 |
| 衰减下限、log 压缩、排除自身 star | 结构性常数 | 不参与权重调优 |

## 8. 数据融合与统一测试集

### 8.1 总体策略

> 分部数据集用于单模块调优和基线验证；合并成两层统一测试集分别用于一阶有监督校准与复杂图特征消融观察；在合并测试集上做联合校准；最终以公开数据调出的配置作为上线初值，再用真实日志或 A/B 测试微调。

**一句话：分部调优定基线，合并测试防倒退，联合校准定初值，真实日志做微调。**

- **分部基线**：T2Ranking 定语义/字面排序基线；DISGCN/Dianping 校准一阶社交与 star/交互；ChronoQA 单独校准时效半衰期；
- **合并测试**：将多源记录映射到统一 Schema，构造第一层（一阶关系 + 语义 + 时效）与第二层（复杂图特征消融）；
- **联合校准**：在第一层上做有监督联合校准，选在多种社交密度配置下都稳定的区间；
- **真实微调**：公开数据配置只作上线初值，真实日志/A/B 测试做增量微调；
- 原相关性标签只继承、不改写；社交关系只做特征列，不写进相关性标签；
- 调参源 / 消融源 / 校验源相互隔离，切分按用户或查询隔离、可追溯；合成社交层只做消融，预设分布不反向优化权重；
- 各来源许可不同，实验数据不上生产、不混入真实用户数据。

> 当前 `datasets/` v2 脚本实现的是「融合候选集 + `neutral`/`joint` 两层」，可视为第一层近似；分部基线与第二层复杂图特征消融尚未实现，登记见 [docs/todo.md](todo.md)。

### 8.2 统一 Schema

```text
(query, article, user_context, relevance, time_constraint)
```

`user_context` 字段建议：

| 字段 | 类型 | 说明 |
|---|---|---|
| `is_self_authored` | bool | 文章是否当前用户所写 |
| `is_collaborator` | bool | 当前用户是否为协作者 |
| `relation_type` | enum | `self_authored` / `own_kb` / `mutual_follow` / `follow` / `collaborator` / `starred_kb` / `two_hop` / `same_community` / `jaccard` / `starred_author` / `public` |
| `relation_distance` | float | Dijkstra 加权距离或跳数派生分数 |
| `author_pagerank` | float | 作者全局影响力 |
| `jaccard_followee` | float | 共同关注重合度 |
| `kb_star_count` | int | 知识库 star 数（已排除自身 star；对应最新方案的 `article_star_count`，本项目为知识库级） |
| `user_star_author_count` | int | 用户 star 过该作者的文章数 |
| `published_at` | timestamp | 数据集字段名；语义映射为产品的时效基准时间（`updated_at`，无则 `created_at`） |
| `updated_at` | timestamp | 最后修改时间 |

`time_constraint` 字段：来自 ChronoQA 的时间限定条件，如绝对时间窗口、相对时间表达、聚合时间范围。

### 8.3 实体映射规则

| 源数据集 | 源实体 | 目标实体 | 映射规则 |
|---|---|---|---|
| T2Ranking | query | query | 直接保留 |
| T2Ranking | passage | article | 直接保留；补充 `published_at` 缺失时按查询时间随机生成 |
| T2Ranking | qrels rel | relevance | 四级相关性继承 |
| DISGCN | user | user | 直接映射 |
| DISGCN | friend | follow 边 | 一阶关注关系 |
| DISGCN | item | article | 物品映射为文章 |
| DISGCN | 交互 | star/阅读信号 | 隐式正例；排除当前用户自身交互的重复奖励 |
| DISGCN | ufi | 社交影响信号 | 好友对物品的影响行为，用于构造社交正例 |
| Dianping | 社交边 | follow 边 | 一阶无向社交关系 |
| Dianping | 评分 | star/交互信号 | 评分映射为隐式相关性 |
| ChronoQA | 时间约束 | time_constraint | 注入 query 或 article 元数据 |
| Douban | 社区关系密度 | 合成图密度参照 | 不进入主训练集，仅用于设定合成社交密度 |
| ZhihuRec | 用户—作者—回答 | 补充校验 | 不进入主训练集，仅校验排序规律 |

### 8.4 第一层：一阶关系 + 语义 + 时效的有监督联合校准

**组成**：

- T2Ranking 提供 query-article 语义相关性标签；
- DISGCN/Dianping 提供一阶社交关系映射与交互信号；
- ChronoQA 提供时间约束与时效符合度标签。

**操作要求**：

1. **分数事前对齐**：不将不同数据集原始分数直接加权；先将每一路分数转换为该 query 候选集内的排名或分位数，或做样本内归一化；融合层优先考虑 RRF；
2. **按用户或查询切分**：不能简单拼接后按行随机切分；以用户 ID 或查询组为最小切分单元，确保测试集中的用户/查询在训练和验证阶段未出现；
3. **联合校准而非简单加权**：
   - 首选：将语义分数、关键词分数、一阶社交特征、时间特征一起作为同一个 LambdaRank 模型的特征列，以 T2Ranking 相关性为主标签联合训练；
   - 次优：使用 RRF 融合各路排名列表，在统一测试集上网格搜索一阶社交权重和时间权重组合，选在多种社交密度配置下都稳定的区间。

**第一层可校准对象**：`w_social` 一阶基线、`w_star`、互关/单向分差、协作者信号、线性融合 `α`、`timeDecay` 半衰期、`RRF` 的 `k`（若扫描）。

### 8.5 第二层：复杂图特征的消融观察层

**组成**：在第一层候选集基础上，加入合成的二跳关系、PageRank 分数、Leiden 社区标签、Jaccard 共同关注重合度等复杂图特征。

**操作要求**：

- **不追求有监督地「学出」这些特征的绝对权重**；
- 扫描各复杂特征的加成系数，观察 NDCG 的**变化方向和敏感区间**；
- 若某特征在较宽系数范围内都带来正贡献，说明稳健；若轻微增大就导致 NDCG 下降，说明当前数据分布下噪声较大，应保守取值或暂不纳入 MVP；
- 合成社交层时，预设多种社交密度配置（如一阶出度、二跳可达性），但不要用预设分布反向优化权重，避免拟合自己的模拟假设；
- Douban 可用于为合成图的社交密度设定提供中文社区参照。

**第二层可确定对象**：二跳是否纳入及安全加成区间、PageRank 加成系数安全区间、Leiden 同社区是否纳入、Jaccard 权重安全区间、Node2Vec 是否值得探索。

### 8.6 切分与防泄漏

1. 以用户 ID 或查询组为最小切分单元；
2. 同一 article 的多用户视角必须落在同一 split，防止对照组跨集泄漏；
3. 跨数据集实体重叠时，优先按用户 ID 去重；若无法去重，按查询语义哈希分组；
4. 训练/验证/测试建议比例 7:1.5:1.5，按用户分层，保证社交密度分布一致；
5. 测试集中的用户/查询必须在训练和验证阶段从未出现。

### 8.7 质量校验清单

| 检查项 | 方法 | 通过标准 |
|---|---|---|
| 分布对比 | 改造前后各特征分布 KS 检验或分位数对比 | 无异常偏移 |
| 标签一致性 | 抽样人工复核 relevance 与 time_constraint | 一致率 ≥ 90% |
| 特征相关性 | 计算社交特征与 relevance 的相关系数 | 存在正相关但不过强（避免捷径） |
| 基线可学习性 | 用纯语义特征训练 LambdaRank | NDCG@10 与 T2Ranking 原始基线接近 |
| 社交信号有效性 | 加入一阶社交特征后 NDCG 是否提升 | 有稳定正贡献 |
| 负样本质量 | 检查 hard negative 是否包含社交近邻 | 避免全部为公开文章 |
| 切分泄漏 | 检查测试集用户/查询是否出现在训练集 | 零重叠 |

### 8.8 工程落地

- **存储格式**：Parquet（列式，适合特征扫描）或 JSONL（便于调试）；当前 v2 产物为 JSONL，Parquet 作为后续列式扫描选项；
- **流水线步骤**：原始数据加载 → Schema 对齐 → 实体映射 → 社交信号注入 → 复杂特征合成 → 标签构造 → 切分 → 质量校验 → 版本归档；
- **版本管理**：每次改造生成 `dataset_version`，记录源数据集版本、映射规则版本、合成参数、切分种子；
- **可复现性**：固定随机种子；映射规则与合成参数写入配置文件；流水线脚本纳入版本控制；
- **细节归属**：下载地址、原始字段、融合脚本、向量缓存、抽查运行细节见 `datasets/README.md`（不入 git）。

### 8.9 分阶段交付

**MVP 阶段**：

- 字段：`query`、`article`、`is_self_authored`、`relation_type`（self_authored/own_kb/mutual_follow/follow/collaborator/starred_kb/starred_author/public）、`kb_star_count`、`published_at`、`relevance`；
- 样本量：T2Ranking 抽样 5~10 万 query-article 对，注入一阶社交关系；
- 验收标准：纯语义基线可复现；加入一阶社交特征后 NDCG@10 有稳定提升；切分无泄漏。

**进阶阶段**：

- 补充字段：`relation_distance`、`author_pagerank`、`jaccard_followee`、`community_id`、`time_constraint`；
- 样本量：扩展至 30 万+ query-article 对，加入 ChronoQA 时间约束；
- 验收标准：复杂图特征消融扫描完成；各特征安全区间确定；两层测试集联合校准完成。

## 9. 自动调参闭环

### 9.1 调参对象分层

| 层级 | 调参对象 | 反馈来源 | 说明 |
|---|---|---|---|
| 数据集融合比例 | 一阶社交密度、star/交互样本比例、时间约束样本比例、正负样本比例 | Qwen3.5-4B 抽样评分 + 人工抽检 | 调整融合数据分布，不直接优化最终召回权重 |
| 召回融合权重 | `w_social`、`w_star`、线性融合 `α`、`timeDecay` 半衰期、互关/单向/协作者档位分差、PageRank 加成系数、`RRF` 的 `k` | Qwen3.5-4B 对最终融合排序评分 + 独立验证集指标 | 优化最终排序质量，保持社交关系主线 |

两层不混在同一轮优化中，避免用同一合成信号反向优化最终权重，造成循环论证。

### 9.2 闭环流程

```text
Step 1：优化器提出一组候选参数（融合比例或召回权重）
  ↓
Step 2：用当前参数生成/排序候选集，得到最终融合排序列表
  ↓
Step 3：Qwen3.5-4B 按固定 rubric 对“最终融合排序”打分
         集成打分 3~5 次取均值/中位数，必要时温度校准
  ↓
Step 4：将评分与独立验证集指标聚合为目标函数
         例如：质量分 + NDCG@10 + SocialNDCG@K
  ↓
Step 5：贝叶斯优化/网格搜索根据反馈提出下一组参数
  ↓
Step 6：重复直至收敛或达到预算
  ↓
Step 7：人工抽检 + 独立验证集复核，写入上线初值
```

### 9.3 Qwen3.5-4B 评分引擎要求

- 配置为**校准评分头/scalar scoring head**，输出校准后的排序质量评分，而非自由生成文本评语；
- 使用**固定评分 rubric**，必须包含：语义相关性、社交关系优先级序关系、时效合理性、star 质量；
- rubric 只规定序关系，不预设等距数值；
- 使用**集成打分** 3~5 次取均值/中位数，降低单次推理噪声；
- 必要时用一小批人工标注样本拟合温度参数，对输出做温度校准；
- 定期用人工标注样本校验评分趋势是否漂移；
- 复用现有本地 Ollama `qwen3.5:4b` 部署，不新增第二个在线模型服务；
- **只输出评分，不直接输出参数数值**；参数增减由优化器完成。

### 9.4 关键原则

1. **模型只输出评分，不直接输出参数数值**；参数增减由优化器完成；
2. Qwen3.5-4B 评估的是**包含社交权重的最终融合排序**，不是只评估语义相关性；
3. Qwen3-Reranker-0.6B 的分数**不直接驱动参数决策**；
4. 固定 rubric 中必须明确社交关系优先级序关系；
5. 使用集成打分、温度校准、人工抽检，降低 LLM 评分噪声与漂移；
6. 结构性常数不参与调优；
7. 优先使用贝叶斯优化替代纯网格搜索，减少评估次数与噪声累积；
8. 最终权重必须由独立验证集与消融确认，不能仅依赖 LLM 评分。

## 10. 调优路线与实验设计

### 10.1 总体策略

> 分部调优定基线，合并测试防倒退，联合校准定初值，真实日志做微调。

### 10.2 分部基线与联合校准

1. **分部基线**：
   - T2Ranking：语义/字面排序基线；
   - DISGCN/Dianping：一阶社交与 star/交互权重基线；
   - ChronoQA：单独扫描时间衰减函数参数；
2. **合并两层**：
   - 第一层：一阶关系 + 语义 + 时效，有监督联合校准；
   - 第二层：复杂图特征消融观察；
3. **联合校准**：
   - 固定函数形式（见 3.3 公式），只调待标定超参（见 6.1）；
   - 粗到细参数搜索：粗搜索定位最佳参数**区间**，细搜索在区间内 + 两侧邻域缩小步幅，独立校验复检边界参数；
   - 可选进阶：以第一层语义标签为主标签，语义/关键词/时效/社交特征作为特征列做 LambdaRank 联合训练，与机械融合 + 粗到细网格结果对比；
   - 门槛：原始分布内各特征至少不差于「不加该特征」的基线（见 10.7 消融）。

### 10.3 权重确定口径

- 参数搜索（含抽查质检）只负责发现系统性问题、定位稳定区间；
- **最终权重由消融实验与独立校验确定**，不由抽查质检结论直接决定；
- 融合数据集分布 ≠ 真实产品分布，调出的权重只是合理初值；
- 可校准：`w_social` 一阶基线、`w_star`、互关/单向分差、协作者信号、线性融合 `α`、timeDecay 半衰期、RRF 的 `k`（若扫描）。

**复杂图特征消融观察**（二跳、PageRank、Leiden、Jaccard）：在整体调参候选集基础上加入合成的二跳关系、PageRank 分数、Leiden 社区标签、Jaccard 共同关注重合度等，用合成社交层 + 消融观察：

- **不追求有监督地「学出」绝对权重**；
- 扫描各复杂特征的加成系数，观察 NDCG 的**变化方向和敏感区间**；
- 若某特征在较宽系数范围内都带来正贡献，说明稳健；若轻微增大就导致 NDCG 下降，说明当前数据分布下噪声较大，应保守取值或暂不纳入 MVP；
- 合成社交层预设多种社交密度配置（一阶出度、二跳可达性），但不要用预设分布反向优化权重。

可确定：二跳是否纳入及安全加成区间、PageRank 加成系数安全区间、Leiden 同社区是否纳入、Jaccard 权重安全区间、Node2Vec 是否值得探索。

### 10.4 粗到细搜索

| 轮次 | 搜索范围 | 步幅 | 抽查量 | 目标 |
|---|---|---|---|---|
| ① 粗搜索 | 全参数网格 | 大步幅（如 `w_social` 取 0/0.1/0.3/0.5/1.0 粗档，档位分差取几组序关系组合，半衰期取几个粗档、以长档为主） | 按数据集规模分层抽样 | 定位最佳参数**区间**：语义切片不明显退化、社交切片有提升、正负样本社交分布不过度分离；不追单点最优 |
| ② 细搜索 | 区间内 + 两侧邻域 | 缩至粗搜索的 1/3–1/5 | 分层抽样 + 全量 NDCG | 确认稳定最优带；检查是否过拟合粗搜索抽样偏差 |
| ③ 收敛验证 | 争议样本 + 边界参数 | — | 争议样本 + 独立校验集 + 全量 NDCG | 复检前两轮争议样本与边界参数；独立校准集与外部校验集确认方向一致；输出最终权重区间与敏感性报告 |

- 区间附近继续搜索（含邻域）防止粗搜索漏掉边界外的更优解；
- 表中数值为**执行参考量级、非推荐权重**；最终权重区间由实验产出。

### 10.5 消融与兜底

如果合并后整体指标下降：

1. 依次移除社交、时间、复杂图特征，观察性能变化；
2. 定位是哪一路信号在合并时引入冲突；
3. 确认单模块是否本身退化；
4. 必要时重新校准该模块参数，再进入整体调参。

### 10.6 上线初值与真实微调

融合数据集调出的权重作为「合理起点」，上线后通过真实用户日志、点击/阅读/star 行为和 A/B 测试做增量微调。

### 10.7 消融实验表

| 方案 | 组成 | 目的 |
|---|---|---|
| Baseline | 纯语义 | 下界 |
| +时效 | 语义 × timeDecay | 时效贡献 |
| +star | + `w_star × starScore` | 质量信号贡献 |
| +一阶社交 | + `w_social × socialScore`（互关/单向/协作者/starred_kb/starred_author） | 一阶社交贡献 |
| +复杂图特征 | 二跳、PageRank、Leiden、Jaccard 等 | 复杂结构边际贡献 |
| Full | 上面 + RRF/线性融合对比 | 融合方式对比 |
| 语义精排对照 | 无重排 / Decima noul / Qwen3-Reranker-0.6B（可选 bge-reranker-v2-m3） | 语义精排边际贡献与延迟 |

指标：NDCG@10、Recall@20、SocialNDCG@K（社交近邻文章排名提升）；ChronoQA 上可衡量时间窗口命中率或时序答案准确率；精排对照额外记录 P95 延迟。

### 10.8 超参来源映射

| 超参 | 标定方式 |
|---|---|
| `w_star` | 分部一阶社交标定 + 两层统一测试集联合校准；粗到细网格/贝叶斯 |
| `w_social` | 同上 |
| `α` / `w_v` / `w_k` / `w_both` | 语义基线与联合校准；粗到细网格/贝叶斯 |
| timeDecay 半衰期 | ChronoQA 单独校准 + 联合校准，按内容类型分组扫描；粗档以长半衰期为主（弱影响约束） |
| 互关/单向分差 | 一阶社交数据构造「单向标互关」变体反推 |
| 协作者信号权重 | 一阶社交数据联合校准 |
| 二跳是否纳入及分差 | 合成社交层 + 消融 |
| PageRank 加成系数 | 合成社交层 + 消融 |
| Leiden 同社区权重 | 合成社交层 + 消融 |
| Jaccard 重合度权重 | 合成社交层 + 消融 |
| `RRF` 的 `k` | 第一层扫描或固定常用值 |
| Node2Vec 是否启用 | 时间允许时对比 |
| 语义精排是否启用/选型 | T2Ranking 候选集对比 + P95 延迟 |
| P95 分位数 | 数据分布统计，非权重，定期刷新 |
| 衰减下限、log 压缩、排除自身 star | 结构性常数，不参与调优 |

## 11. 风险与边界

1. **公开数据集无法复现真实产品分布**：调出的权重只是起点。
2. **合并测试集不能替代真实标注**：复杂图特征来自模拟映射，适合相对排序、回归验证和敏感性分析，不适合直接学绝对权重。
3. **不同数据集分数尺度不可比**：必须排名化/分位数化/样本内归一化，优先 RRF。
4. **切分必须按用户或查询隔离**：按行随机切分会导致同一用户/查询泄漏，虚高指标。
5. **复杂图特征不能从公开社交推荐数据集直接有监督学习**：一阶用公开数据，二跳/PageRank/Leiden/Jaccard 用合成社交层 + 消融。
6. **不要用模拟分布反向优化权重**：保留多种社交密度配置做敏感性分析。
7. **star 口径差异**：项目是知识库级 star，公开数据是物品评分/交互，映射只能近似，归一化参数不能直接复用。
8. **时效字段有限**：当前只有 `created_at` / `updated_at`，没有内容级更新时间；`updated_at` 可能被元数据编辑刷新，必要时后续加 `content_updated_at`。
9. **gograph 生态风险**：当前 v0.17.x 为 pre-1.0、生态较小；必须 pin 版本、adapter 隔离，保留 `gonum/graph` 或自研 CSR + BFS 的退路。
10. **图服务一致性/降级**：多副本各自重建可能短暂不一致；AI 侧必须有 socialScore=0 或旧五档回退。
11. **重排序覆盖社交权重风险**：Qwen3-Reranker-0.6B 只做语义路精排，禁止最终融合后全局重排。
12. **LLM 评分噪声与校准偏差**：Qwen3.5-4B 必须使用固定 rubric、集成打分、温度校准和人工抽检；只评分不输出参数。
13. **循环论证风险**：融合比例参数可由 LLM 质检辅助调整，但最终召回权重不能仅依赖同一合成信号优化；需保留独立验证集和人工标注样本。
14. **0.6B 精度上限**：0.6B 弱于 4B，难例语义可能不足；上线前必须在 T2Ranking 候选集对比「无重排 / Decima noul / 0.6B」，无稳定提升就不开在线。
15. **延迟与成本**：0.6B 虽轻，在线精排仍会增加延迟；默认先离线质检，在线使用需单独评估 P95 延迟与吞吐。
16. **角色混淆风险**：Qwen3-Reranker-0.6B 不能替代 Decima-small 做 Agent 选择；Qwen3.5-4B 不应进入在线最终决策链路。
17. **参数纪律**：结构性常数不参与调优；待标定超参必须有实验来源；关系优先级只给序关系，不预设等距数值。
18. **Agent 选择优先级较低**：R3-Skill / AgentSelect 可在召回权重调优完成后用于 Decima-small 的 Agent 路由或提示词优化，不纳入当前核心调优链路。

## 12. 分阶段落地

### MVP

- 只记 `self_authored` / `own_kb` / `mutual_follow` / `follow` / `collaborator` / `starred_kb` / `starred_author` / `public`；
- BFS 算 1~2 跳；
- 优先级序关系 + 线性融合或 RRF；
- 语义精排先离线质检（Qwen3-Reranker-0.6B），在线开关按 P95 延迟评估；
- 用分部基线 + 第一层统一测试集初步标定 `w_social`、`w_star`、互关/单向分差、时间半衰期；
- 快速验证「社交感知是否提升召回」。

### 进阶

- 加入 PageRank、Leiden、Jaccard；
- 融合升级为 RRF/rerank 对比；
- 用合成社交层做复杂图特征消融；
- 做完整消融与超参标定；
- 接入 Qwen3.5-4B 离线自动调参闭环；
- 完成第二层消融观察集。

### 可选

Node2Vec 嵌入作为「时间允许」的探索项；`gograph` + BFS/PageRank 组合已足以覆盖 MVP 的关系层级。

## 13. 一句话结论

> 语义精排只做语义召回路上的语义分：Qwen3-Reranker-0.6B 在向量/关键词召回之后、时效与 star/社交融合之前输出 `rerankScore`，排名化后作为 `semanticScore`，再乘时间衰减、加 star 与社交权重做最终排序；绝不能在最终融合后做全局重排。Decima-small 只管 Agent 选择，Qwen3-Reranker-0.6B 管语义信号增强，Qwen3.5-4B 管离线评估「含社交权重的最终排序」；T2Ranking 定语义基线，DISGCN/Dianping 标定一阶社交与 star 权重，ChronoQA 单独校时效半衰期；复杂图特征用合成社交层 + 消融确定安全区间，最终以公开数据配置为初值，用真实日志和 A/B 测试完成微调。

> 执行跟踪见 [docs/todo.md](todo.md)；服务实现见 [service/social-graph/docs/architecture.md](../service/social-graph/docs/architecture.md)。
