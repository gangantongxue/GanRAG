# 社交关系感知召回与权重标定

> **定位**：P3 社交召回的设计与调优总纲。服务实现见 [service/social-graph/docs/architecture.md](../service/social-graph/docs/architecture.md)，消费端接入见 [service/ai/docs/architecture.md](../service/ai/docs/architecture.md)「多路召回与聚合审查」。
>
> **权重纪律**：本文只给**相对优先级与调优方法**，不写具体权重数值。融合权重、关系档位分差、时效半衰期、PageRank/Jaccard/社区加成系数、RRF 的 `k` 等均为**待标定超参**，由公开数据集实验确定，上线后再用真实日志与 A/B 测试微调；归一化封顶、衰减下限、`log1p` 压缩、排除自身 star、`1/(1+distance)` 等为**结构性常数**，不参与权重调优。

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
- 召回排序可解释、可配置、可消融、可降级；
- 用公开数据集完成第一轮权重标定，真实日志/A/B 测试做最终微调。

### 1.3 不做什么

- **不引入文章级 star**：当前产品的 star 语义是「star 知识库」，文章热度继承所属知识库；如未来产品确需文章级 star，另立迁移与接口，不在本次范围。
- **不新增 follow-kb 表**：关注知识库就是 star 该库；关注用户仍走 `user_follows`。
- **不让社交信号绕过权限**：权限硬过滤永远先于任何召回与排序。
- **不在 P3 上线 Node2Vec/GNN**：作为时间允许时的进阶对比项。
- **不把公开数据集调出的权重当生产最优值**：它只是合理初值。
- **不调整 Agent 选择模型与提示词**：R3-Skill / AgentSelect 后置，不纳入召回权重调优链路。

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
| 二跳关注 | 无显式边 | BFS 离线计算可达集合，不存边 |
| 发布时间 | `articles.created_at` | 视为发布时间 |
| 最后修改时间 | `articles.updated_at` | 作为新鲜度/最后修改时间；如后续需要内容级更新时间，再加字段 |
| 用户/知识库/文章标识 | 对外统一 xid | 社交图服务只用 `user_xid` / `kb_xid` / `article_xid`，自增 ID 不出服务 |
| 关注库主 | `user_follows` 可独立存在 | 只有显式关注用户时才算 `FOLLOWED_AUTHOR`，不由 star 推导 |

**关键区分**：

- `STARRED_KB`：我 star 了这篇文章所属的知识库，是**直接关系**；
- `STARRED_AUTHOR`：我 star 过该作者拥有的其他知识库，是**弱作者聚合信号**；
- `FOLLOWED_AUTHOR`：我显式关注了该作者/库主，是**用户关注关系**，与 star 是两件事。

## 3. 召回流程

### 3.1 处理顺序

```text
权限硬过滤
→ 向量召回 Top-Nv + 关键词召回 Top-Nk
→ 合并去重 → 排名归一化或 RRF
→ 时效乘性衰减
→ star / 社交加性融合
→ 取 Top-K → Decima-small 符合度审查
→ 最终排序
```

权限过滤必须先于两条召回链路：私有库排除非成员，公开库允许所有人；未授权文档不得进入候选集，也不得发送给 Decima-small。

### 3.2 公式（符号化）

```text
R(d)      = Fuse(V(d), K(d))                  // 排名归一化或 RRF
B(d)      = R(d) × timeDecay(d)               // 时效乘性
S_pre(d)  = B(d)
          + w_star   × starScore(kb(d))
          + w_social × socialScore(u, d)      // 社交分数，见第 4 节
S_final(d)= S_pre(d) + w_audit × audit(d)     // Decima 审查分，作用在 Top-K
```

其中：

- `V(d)` / `K(d)`：向量、关键词两路在该 query 候选集内的排名归一化分数；未命中的链路记 0；
- `Fuse` 可选线性融合或 RRF：
  - 线性：`R = w_v × V + w_k × K + w_both × A`，`A` 为双路命中标记；
  - RRF：`R = Σ 1/(k + rank_i(d))`，对量纲不敏感，适合社交分分布不均时兜底；
- `timeDecay`：按 `created_at` / `updated_at` 计算，带结构性下限，防止经典老文章被完全压死；
- `starScore`：知识库 star 数的分位数归一化 + `log1p` 压缩 + P95 封顶；
- `socialScore`：用户 × 文章的社交分数，完全由社交图服务离线算好、在线查表；
- `audit`：Decima-small 对 Top-K 候选的 `noul` 符合度审查分；只作为相关性信号，不绕过权限、不替代召回链路。

### 3.3 为什么时效用乘性、star/社交用加性

- **时效乘性**：过时文章语义再相关也应被压降，而不是被高语义分抵消；这是结构性选择。
- **star/社交加性**：二者可相互补偿——社交远但 star 极高仍值得推，社交近但 star 一般也应有机会。
- **Decima 审查**：作为独立的符合度信号，放在 Top-K 复核，权重单独标定；不与社交/star 混为一个分数。

### 3.4 候选元数据契约

AI 服务在向量写入/检索时需要拿到以下字段（不可变字段可随向量 metadata 写入，动态字段查询时从 Repository 或缓存取）：

| 字段 | 来源 | 用途 |
|---|---|---|
| `article_xid` / `kb_xid` | 向量 metadata | 候选标识、权限过滤 |
| `author_xid` | `articles.created_by_xid` | 作者关系、社交分 |
| `kb_owner_xid` | `knowledge_bases.owner_xid` | 库主关系、社交分 |
| `created_at` / `updated_at` | `articles` | 时效衰减 |
| `kb_star_count` | `knowledge_bases.star_count` | star 热度（动态，查询时取最新值） |

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

- `mutual_follow` 不单独存边：建图时保留两条有向 `follow` 边，计算时检测双向；Dijkstra 边权（互关成本 < 单向成本）是待标定超参，二跳由 BFS 单独计算。
- `collaborator` 由 KB 成员关系派生，不依赖文章共同作者表。
- `starred_author` 是用户→作者的弱信号，不建用户-用户边；在文章中通过 `author_xid` 查询，计数先做分档/归一化再映射为档位分，避免无上限放大。
- `starred_kb` 与 `follow` 独立：star 库不等于关注库主。

### 4.3 结构派生

| 算法 | 用途 | 计算时机 |
|---|---|---|
| BFS 1~2 跳 | 二跳关注可达集合（关注的人的关注、关注我的人关注的人） | 离线全量/定时 |
| PageRank | 用户全局影响力，作为所有档位的加成项 | 离线全量/定时 |
| Leiden | 社区归属，同社区弱加成 | 离线全量/定时 |
| Jaccard | 共同关注重合度（限 2 跳内） | 离线全量/定时 |
| Dijkstra | 同跳数但关系强度不同时的加权距离（进阶） | 离线全量/定时 |

- 二跳**不显式存边**，避免边数爆炸；离线算好 `map[user]reachable`。
- PageRank 作用在关注/协作等用户图上，作为**加成项**，不替代直接关系档位。
- Leiden 在无向投影上运行；同社区只做弱辅助信号。
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

- 上表只保证排序，不保证相邻档位等距；实际分差由数据集实验反推。
- `self_authored` 与 `own_kb` 默认同属最高档；如产品需要，可在档位内再细分。
- `PageRank` 是所有档位之上的加成项，不是独立档位。
- `starred_author` 是弱作者聚合信号，低于同社区/Jaccard 等结构信号。

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
AI 服务：权限过滤后的候选 + 社交分 → 最终排序
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
| `w_social` | 社交分数融合权重 | 第一层联合校准 |
| `w_star` | star 热度融合权重 | 第一层联合校准 |
| `w_audit` | Decima 审查分权重 | 第一层/Top-K 复核实验 |
| `w_v` / `w_k` / `w_both` 或 `α` | 语义、关键词、双路命中的融合比例 | T2Ranking / DuReader |
| 关系档位分差 | 互关、单向、协作者、starred_kb、二跳等实际分差 | Dianping/Douban + 合成变体 |
| `timeDecay` 半衰期/衰减函数 | 时效衰减速度 | ChronoQA |
| PageRank 加成系数 | 影响力加成强度 | 第二层消融 |
| 社区/Jaccard 系数 | 结构辅助信号强度 | 第二层消融 |
| `RRF` 的 `k` | RRF 平滑参数 | 第一层扫描或固定常用值 |
| 二跳是否纳入 | 二跳是否有正贡献 | 第二层消融 |
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
- Top-Nv / Top-Nk / Top-K 等候选规模（可作为工程配置，但不是权重）。

### 6.3 配置归属

- 权重与阈值由 **AI 服务配置**持有，不存数据库；
- 社交图服务只产出分数/画像，不决定最终排序；
- 每次实验记录配置版本、数据集版本、切分方式与指标，保证可复现。

## 7. 公开数据集选型

### 7.1 核心边界

公开数据集中**没有**直接带「用户社交关系 + 知识库文章 + 相关性标注」三元组的中文基准；社交推荐数据集通常只原生支持一阶关系，无法直接提供二跳、PageRank、Leiden、Jaccard、Node2Vec 的标注。因此：

- **一阶社交 + star/交互信号**：用公开社交推荐数据集做有监督校准；
- **复杂图特征**：用「语义基准 + 合成社交层 + 消融扫描」确定边际贡献与安全区间，不直接学习绝对权重。

### 7.2 数据集角色

| 数据集 | 主要用途 | 优先级 | 使用方式 |
|---|---|---|---|
| [T2Ranking](https://github.com/THUIR/T2Ranking) | 语义/关键词排序基线；训练 LambdaRank | 最高 | 30 万+ query、200 万+ passage，四级相关性 qrels；Apache-2.0 |
| [Dianping（Dianping_SocialRec / DISGCN 论文使用）](https://ieeexplore.ieee.org/document/9827970/) | 一阶关注关系 + 物品交互，校准一阶社交与 star/交互信号 | 高 | 用户-好友-物品映射为「用户-关注-文章」；多种社交密度 |
| [Douban](https://douban.datasets.syr.edu/) | 中文社区关系密度与内容交互模式参照 | 中 | 校验一阶权重合理性，为合成图社交密度提供参照 |
| [ChronoQA](https://arxiv.org/abs/2508.12282) | 单独校准时效性权重 | 高 | 30 万+ 新闻（2019-2024）、5,176 个中文问题，覆盖绝对/聚合/相对时间 |
| [DuReader_retrieval](https://github.com/baidu/DuReader) | 补充测试集，验证语义/字面排序泛化性 | 中 | 9 万+ query、800 万+ passage；不主导调参 |
| [ZhihuRec](https://github.com/THUIR/ZhihuRec-Dataset) | 中文内容社区「用户—作者—回答」排序规律补充校验 | 中 | 轻量补充验证，不纳入核心调优链路 |
| R3-Skill / AgentSelect | Agent 选择模型微调或路由评估 | 低 | 放在召回权重调优之后，不作为当前重点 |

> **口径修正**：DISGCN 是「Disentangled Social Graph Convolutional Network」方法，不是数据集；本项目使用其论文中的 Dianping/Douban 社交推荐数据作为一阶关系校准来源。

**核心主线**：T2Ranking + Dianping/Douban + ChronoQA。

- T2Ranking：语义/字面相关性基线；
- Dianping/Douban：一阶社交关系与 star/交互信号的融合权重；
- ChronoQA：发布时间/最后修改时间的时效衰减参数。
- DuReader、ZhihuRec 做补充校验；R3-Skill / AgentSelect 后置。

### 7.3 能调什么、不能调什么

| 调优对象 | 可用数据集/方法 | 说明 |
|---|---|---|
| 语义与关键词排序基线 | T2Ranking、DuReader_retrieval | 有监督 LambdaRank；四级相关性标签 |
| `w_social` 一阶基线量级 | Dianping、Douban | 一阶关注/互关 + 交互信号联合校准 |
| `w_star` 与社交相对关系 | Dianping、Douban | 评分/交互作为隐式质量信号；项目 star 是 KB 级，映射为近似 |
| 互关 vs 单向分差 | Dianping/Douban 构造变体 | 将部分单向边标记为互关，观察 NDCG 变化反推量级 |
| 协作者信号 | Dianping/Douban 共同交互近似 | 以「共同交互过同一物品」近似「同一 KB 成员」 |
| 时效半衰期 | ChronoQA | 固定其他特征，扫描指数/线性衰减参数；看时间窗口命中或时序答案准确率 |
| 二跳是否纳入及分差 | T2Ranking 合成社交层 + 消融 | 公开数据无原生二跳标注；扫描二跳加成系数，观察 NDCG 方向与敏感区间 |
| PageRank 加成系数 | 合成社交层 + 消融 | 扫描加成系数，确定稳健区间，不追求绝对权重 |
| Leiden 同社区贡献 | 合成社交层 + 消融 | 观察同社区是否有正贡献；决定是否进入 MVP 之后的进阶阶段 |
| Jaccard 共同关注重合度 | 合成社交层 + 消融 | 观察边际贡献与敏感区间 |
| RRF 的 `k` | 第一层扫描或固定常用值 | 可作结构性常数或超参扫描 |
| Node2Vec 是否启用 | 时间允许时对比 | 对比嵌入信号是否优于纯图分数，不阻塞主线 |
| P95 归一化封顶 | 数据分布统计 | 统计参数，不是权重；需随数据定期刷新 |
| 衰减下限、log 压缩、排除自身 star | 结构性常数 | 不参与权重调优 |

### 7.4 数据边界

- 公开社交推荐数据集不是知识库 RAG 分布，调出的权重只能作为初值；
- Dianping 的评分/交互不等于项目 star，映射存在语义差；
- 项目 star 是知识库级，不能直接复用文章级 star 数据集的归一化参数；
- 合成社交层的密度、二跳可达性由实验预设，**不能用预设分布反向优化权重**；
- 切分必须按用户或查询隔离，不能按行随机切分；
- 各数据集许可不同，实验数据不上生产、不混入真实用户数据。

## 8. 调优路线

### 8.1 总体策略

> 分部调优定基线，合并测试防倒退，联合校准定初值，真实日志做微调。

### 8.2 阶段 1：分部调优

1. **语义/关键词排序**
   - T2Ranking 训练 LambdaRank 基线；
   - 向量相似度、关键词匹配分数作为特征，四级相关性为 label；
   - DuReader_retrieval 作为补充测试集验证泛化性；
   - 得到纯相关性基线，作为后续引入社交/时效的锚点。

2. **一阶社交与 star**
   - Dianping/Douban 模拟知识库场景：用户—好友映射为关注，物品映射为文章，交互映射为阅读/点赞/star；
   - 构造多种社交密度配置，扫描互关、单向、协作者、starred_kb、starred_author 的相对比例；
   - 重点观察不同密度下 NDCG@K、MAP 的稳定性；
   - Douban 做中文社区密度参照。

3. **时效性**
   - ChronoQA 单独拟合时间衰减函数参数；
   - 固定其他特征，只改变指数衰减半衰期、线性衰减斜率等；
   - 观察检索文档是否落在问题要求的时间窗口，或端到端时序答案准确率。

**门槛**：每个模块在原始数据分布内，至少不差于「不加该特征」的基线。

### 8.3 阶段 2：两层统一测试集

统一测试集适合回答「合并后有没有性能倒退」、比较「哪组权重在多种社交密度下更稳定」；**不适合直接学习全部社交参数的绝对权重**，因为复杂图特征信号是模拟注入的。

#### 第一层：一阶关系 + 语义 + 时效的有监督联合校准

组成：

- T2Ranking：query-article 语义相关性标签；
- Dianping/Douban：一阶社交关系与交互信号；
- ChronoQA：时间约束与时效符合度标签。

统一 Schema：

```text
(query, article, user_context, relevance, time_constraint)
```

`user_context` 容纳：文章作者是否为当前用户、KB 所有者是否为当前用户、是否为 KB 成员、是否 star 该 KB、是否关注作者/库主、互关/单向、知识库 star 数、发布时间、最后修改时间等。

操作要求：

1. **分数事前对齐**：不同数据集的原始分数不直接加权；先转成该 query 候选集内的排名/分位数，或做样本内归一化；融合层优先考虑 RRF。
2. **按用户或查询切分**：以用户 ID 或查询组为最小切分单元，确保测试集用户/查询未在训练/验证中出现，避免信息泄漏。
3. **联合校准而非简单加权**：
   - 首选：语义、关键词、一阶社交、时间特征一起作为同一个 LambdaRank 的特征列，以 T2Ranking 相关性为主标签联合训练；
   - 次优：RRF 融合各路排名列表，在统一测试集上网格搜索一阶社交/时间权重组合，选在多种社交密度下都稳定的区间。

第一层可校准：`w_social` 一阶基线、`w_star`、互关/单向分差、协作者信号、线性融合 `α`、timeDecay 半衰期、RRF 的 `k`（若扫描）。

#### 第二层：复杂图特征消融观察层

在第一层候选集基础上，加入合成的二跳关系、PageRank 分数、Leiden 社区标签、Jaccard 共同关注重合度等。

操作要求：

- **不追求有监督地「学出」绝对权重**；
- 扫描各复杂特征的加成系数，观察 NDCG 的**变化方向和敏感区间**；
- 若某特征在较宽系数范围内都带来正贡献，说明稳健；若轻微增大就导致 NDCG 下降，说明当前数据分布下噪声较大，应保守取值或暂不纳入 MVP；
- 合成社交层预设多种社交密度配置（一阶出度、二跳可达性），但不要用预设分布反向优化权重；
- Douban 可用于为合成图社交密度设定提供中文社区参照。

第二层可确定：二跳是否纳入及安全加成区间、PageRank 加成系数安全区间、Leiden 同社区是否纳入、Jaccard 权重安全区间、Node2Vec 是否值得探索。

### 8.4 阶段 3：消融与兜底

如果合并后整体指标下降：

1. 依次移除社交、时间、复杂图特征，观察性能变化；
2. 定位是哪一路信号在合并时引入冲突；
3. 回到分部数据确认单模块是否本身退化；
4. 必要时重新校准该模块参数，再进入联合校准。

### 8.5 阶段 4：上线初值与真实微调

公开数据调出的权重作为「合理起点」，上线后通过真实用户日志、点击/阅读/star 行为和 A/B 测试做增量微调。

### 8.6 消融实验表

| 方案 | 组成 | 目的 |
|---|---|---|
| Baseline | 纯语义 | 下界 |
| +时效 | 语义 × timeDecay | 时效贡献 |
| +star | + `w_star × starScore` | 质量信号贡献 |
| +一阶社交 | + `w_social × socialScore`（互关/单向/协作者/starred_kb/starred_author） | 一阶社交贡献 |
| +复杂图特征 | 二跳、PageRank、Leiden、Jaccard 等 | 复杂结构边际贡献 |
| Full | 上面 + RRF/线性融合对比 | 融合方式对比 |

指标：NDCG@10、Recall@20；另可衡量「社交近邻文章」的排名提升；ChronoQA 上衡量时间窗口命中率或时序答案准确率。

### 8.7 超参来源映射

| 超参 | 标定方式 |
|---|---|
| `w_star` | Dianping/Douban 一阶交互 + T2Ranking 联合校准；网格搜索/贝叶斯优化 |
| `w_social` | 同上 |
| `w_audit` | Top-K 复核实验 |
| `α` / `w_v` / `w_k` / `w_both` | T2Ranking + 一阶社交合并测试集 |
| timeDecay 半衰期 | ChronoQA 按内容类型分组扫描 |
| 互关/单向分差 | Dianping/Douban 构造「单向标互关」变体反推 |
| 协作者信号权重 | Dianping/Douban 共同交互近似 |
| 二跳是否纳入及分差 | T2Ranking 合成社交层 + 消融 |
| PageRank 加成系数 | 合成社交层 + 消融 |
| Leiden 同社区权重 | 合成社交层 + 消融 |
| Jaccard 重合度权重 | 合成社交层 + 消融 |
| RRF 的 `k` | 第一层扫描或固定常用值 |
| Node2Vec 是否启用 | 时间允许时对比 |
| P95 分位数 | 数据分布统计，非权重，定期刷新 |
| 衰减下限、log 压缩、排除自身 star | 结构性常数，不参与调优 |

## 9. 风险与边界

1. **公开数据集无法复现真实产品分布**：调出的权重只是起点。
2. **合并测试集不能替代真实标注**：复杂图特征来自模拟映射，适合相对排序、回归验证和敏感性分析，不适合直接学绝对权重。
3. **不同数据集分数尺度不可比**：必须排名化/分位数化/样本内归一化，优先 RRF。
4. **切分必须按用户或查询隔离**：按行随机切分会导致同一用户/查询泄漏，虚高指标。
5. **复杂图特征不能从公开社交推荐数据集直接有监督学习**：一阶用公开数据，二跳/PageRank/Leiden/Jaccard 用合成社交层 + 消融。
6. **不要用模拟分布反向优化权重**：保留多种社交密度配置做敏感性分析。
7. **star 口径差异**：项目是知识库级 star，公开数据是物品评分/交互，映射只能近似。
8. **时效字段有限**：当前只有 `created_at` / `updated_at`，没有内容级更新时间；`updated_at` 可能被元数据编辑刷新，必要时后续加 `content_updated_at`。
9. **gograph 生态风险**：当前 v0.17.x 为 pre-1.0、生态较小；必须 pin 版本、adapter 隔离，保留 `gonum/graph` 或自研 CSR + BFS 的退路。
10. **图服务一致性/降级**：多副本各自重建可能短暂不一致；AI 侧必须有 socialScore=0 或旧五档回退。
11. **Agent 选择后置**：R3-Skill / AgentSelect 不纳入当前调优链路。

## 10. 分阶段落地

### MVP

- 只记 `self_authored` / `own_kb` / `mutual_follow` / `follow` / `collaborator` / `starred_kb` / `starred_author` / `public`；
- BFS 算 1~2 跳；
- 优先级序关系 + 线性融合或 RRF；
- 用第一层合并测试集初步标定 `w_social`、`w_star`、互关/单向分差、时间半衰期；
- 快速验证「社交感知是否提升召回」。

### 进阶

- 加入 PageRank、Leiden、Jaccard；
- 融合升级为 RRF/rerank；
- 用第二层合成社交层做复杂图特征消融；
- 做完整消融与超参标定。

### 可选

Node2Vec 嵌入作为「时间允许」的探索项；`gograph` + BFS/PageRank 组合已足以覆盖 MVP 的关系层级。

## 11. 一句话结论

T2Ranking 定语义基线，Dianping/Douban 标定一阶社交与 star，ChronoQA 校时效；二跳、PageRank、Leiden、Jaccard 等复杂图特征用「T2Ranking 合成社交层 + 消融」确定边际贡献与安全区间；公开数据调出的配置只作上线初值，最终用真实日志/A/B 测试微调。

> 执行跟踪见 [docs/todo.md](todo.md)；服务实现见 [service/social-graph/docs/architecture.md](../service/social-graph/docs/architecture.md)。
