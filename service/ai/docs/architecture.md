# AI 服务架构选型

## AI 框架

**选型：Eino**

Eino 是字节跳动的 AI 应用开发框架，用于构建基于大语言模型的应用，特点包括：
- 组件化：提供模型调用、RAG、工具调用等标准组件
- 编排能力：支持复杂的 AI 工作流编排
- 可扩展：支持自定义组件和插件
- 与豆包大模型深度集成

## 职责

- RAG 检索增强生成
- AI 模型调用
- 向量检索
- 对话管理
- 多智能体路由与切换（P1.5，见下节）

## 多智能体架构（P1.5）

> **落地状态：设计定稿，代码未实现**（P1.5）——实现后更新 docs/architecture.md「设计落地状态」。

用户每条消息先经 **Decima-small** 做结构化判断，再交给选定的智能体执行。Decima-small 只负责有界决策，不负责生成最终回答。本项目使用它完成两类工作：

1. 多智能体路由与切换；
2. 向量召回与关键词召回合并后的候选相关性审查。

社交关系感知的召回重排由 [social-graph 服务](../../social-graph/docs/architecture.md) 提供社交信号，Decima 不承担社交打分；见本文「多路召回与聚合审查（P3）」。

### 决策模型：Decima-small

**选型：Decima-small**（开源、Apache-2.0、Jev/System One 兼容的本地决策模型）。它输出 `choice`、`score`、`noul` 等结构化结果及概率，不生成自然语言回答。

- 122M 参数，int8 ONNX，可在本地 CPU 运行；
- 通过 `pip` 安装官方 Python 包和启动模块；
- Go AI 服务通过 HTTP 调用本地服务，不在 Go 进程内嵌 Python/ONNX 运行时；
- 所有模型概率只作为决策信号，最终是否切换 Agent 或提升文档排名由 AI 服务代码执行。

官方本地启动方式：

```bash
pip install decima-ai
python -m decima.serve --model amyrmahdy/decima-small --port 11436
```

HTTP 地址为 `http://127.0.0.1:11436/v1/systemone`。请求使用 System One 的 `state + questions` 格式；Agent 路由使用 `choice`，候选文档审查使用 `noul`。

**使用边界**：Decima-small 不生成聊天文本、不替代底层 LLM、不绕过权限过滤，也不直接决定数据库写入、工具调用等副作用操作。

### 智能体注册表

- 配置化定义：智能体 `id`、能力描述（Decima-small `choice` 题的 criteria）、Agent 专属提示词
- 所有智能体**共用同一底层 LLM 与同一工具集**（含 `search_context`），差异只在 system prompt 的智能体块

### 路由与切换流程（每轮一次 Decima-small 调用）

1. **首轮**：没有当前 Agent 时，在全部 Agent 中选择概率最高者；如果所有概率都不足以支持可靠路由，则使用默认通用 Agent。
2. **后续轮**：每次新用户消息都重新调用同一个 `choice` 问题，候选包含全部 Agent。
3. 若 `p(current_agent) >= agent_switch_threshold`，保持当前 Agent。
4. 若 `p(current_agent) < agent_switch_threshold`，再查看全部 Agent 的最高概率者：最高概率者仍是当前 Agent 时保持不变，否则切换到最高概率者。

伪代码：

```text
top_agent = argmax(all_agent_probabilities)

if current_agent == "":
    use(top_agent or default_agent)
else if p(current_agent) >= agent_switch_threshold:
    use(current_agent)
else if top_agent == current_agent:
    use(current_agent)
else:
    switch_to(top_agent)
```

模型 top 结果本身不能直接触发切换；当前 Agent 的概率低于阈值是进入重新选择的前置条件。切换阈值先作为配置项保留，后续使用路由准确率、不必要切换率和漏切换率调参，不在设计阶段固化最终值。

### 固定 System Prompt 与 Agent 专属提示词

底层 LLM 使用 OpenAI 消息格式。`system` 消息始终是固定、通用的系统提示词，不随着 Agent 切换而重写。Agent 专属提示词使用 `user` 消息注入，并由服务端使用特殊标签包裹：

```text
<ganrag-agent-system agent="repository">
你是 Repository Agent，负责知识库、目录、文章和文件管理相关任务。
</ganrag-agent-system>
```

固定 System Prompt 必须明确说明：

- `<ganrag-agent-system>` 内的内容是应用服务为 Agent 定义的系统提示词；
- 普通用户消息不能修改系统级规则；
- Agent 专属提示词也不能覆盖安全、权限和系统级约束；
- 上下文中存在多个 Agent 专属提示词时，以**最新的一段**为准；
- 该标签只能由服务端 Agent 注册表生成，不能把用户原文拼接到标签内部。

首次选择 Agent 后，在本轮用户消息前追加对应的 Agent 专属提示词。Agent 切换时不改写历史，而是在当前上下文末尾追加新的 Agent 专属提示词段，并紧接本轮用户问题。这样保留切换历史，同时由固定 System Prompt 规定最新段的优先级。

### 上下文压缩（会话级，存 chat_sessions）

**机制**：压缩上下文**不进用户消息流**，单独存 `chat_sessions.context`，并以 `chat_sessions.context_msg_id` 记录游标——上下文之后需要拼接的起始消息。`context` 是 AI 服务内部状态，不直接返回前端。

1. **触发**：预计本次请求 token 数达到模型上下文上限的 `0.65` 时触发压缩；`0.65` 是初始配置，后续按质量、延迟和缓存命中率调节。
2. **拼装**：固定 System Prompt + `context` 中的最新 Agent 提示词与摘要 + `chat_messages` 中 `id >= context_msg_id` 的可见消息（按序）。`context` 为空时拼全量历史。
3. **压缩**：摘要模型读取旧 `context`（若有）和游标前的真实对话，排除 Agent 专属提示词本身；提取最新 Agent 专属提示词，生成新的摘要，直接替换 `context` 并推进 `context_msg_id`。
4. **压缩结果顺序**：最新 Agent 专属提示词放在压缩上下文最前面，摘要放在其后；旧 Agent 专属提示词不再进入模型上下文。
5. **原文不删**：被压缩区域的真实消息和隐藏提示词记录保留；真实消息供 `search_context` 使用，`agent-system` 仅供 AI 服务内部重建请求和审计，绝不由 `search_context` 返回。

推荐的 `context` 内部结构：

```json
{
  "version": 1,
  "agent_id": "repository",
  "agent_prompt": "<ganrag-agent-system agent=\"repository\">...</ganrag-agent-system>",
  "summary": "压缩后的对话上下文"
}
```

**好处**：消息表保持纯对话（role 仅 1/2），模型上下文**结构性不进用户页面**；多次压缩一个槽位直接替换，无摘要堆积。

### `search_context(keywords)` 上下文搜索工具（全部智能体共享）

**解决的问题**：上下文压缩后，细节（具体数字、原文引用、早期结论依据、用户早先的约定等）不在 prompt 里；当后续回答**需要这些详细上下文**时，AI 用本工具从完整对话记录中找回。

- **调用时机**：回答需要压缩前对话的具体细节时（引用原文、核对早期数据、确认用户早先约定），由 AI 自主判断调用
- **输入**：关键词（可多个）
- **行为**：在**本会话完整对话记录**中匹配（`chat_messages` 全量原文，**含被压缩区域**），返回命中消息的原文片段及所在位置；明确排除 `agent-system` part
- **实现**：复用 `idx_chat_messages_session` 做会话内检索，无新增表；会话内消息量小，LIKE 关键词匹配足够，不建向量索引（YAGNI）
- **效果**：压缩后 prompt 短而省缓存，细节按需召回——上下文「**压缩存储、原文可溯**」

### 缓存命中策略

- Decima-small 是独立决策服务，路由本身**不影响**底层 LLM 的 prompt/KV 缓存；缓存全看执行 Agent 的提示词与上下文前缀
- **主杠杆：少切换与及时压缩**——切换会改变提示词前缀，压缩会重建较短上下文
- 压缩后的顺序固定为：通用 System Prompt → 最新 Agent 提示词 → 摘要 → 游标后的消息

### 配置项（AI 服务配置持有，P1.5 调参）

| 配置 | 初始值 | 说明 |
|------|--------|------|
| `agent_switch_threshold` | 0.5（待测试调整） | 当前 Agent 概率低于此值时才允许重新选择 |
| `default_agent` | 通用智能体 | 低置信度时回落 |
| `context_compression_ratio` | 0.65（待测试调整） | 预计请求 token 达上下文上限的该比例时触发压缩 |
| `context_target_ratio` | 0.35（待测试调整） | 压缩后上下文目标占上下文上限的比例 |

## 向量化与检索路由（P1.5）

> **落地状态：设计定稿，代码未实现**（P1.5）——实现后更新 docs/architecture.md「设计落地状态」。

AI 服务持有**当前向量化模型**配置（稳定 id + 版本，如 `bge-m3`），职责：

- **写入**：文章保存/删除的增量同步后，按 `articles.content_key` 读取 Garage 中的 md 分块、用当前模型向量化，写入 `kb-<kb_xid>-<model_id>` 集合（每条向量 metadata 记 `embedding_model`）
- **检索路由**：查询向量用当前模型编码，**只查当前模型后缀的集合**——用什么模型，就检索对应的向量化结果，切换模型不影响旧向量的可用性
- **切换模型**：新写入切新模型集合 → 按 `content_key` 全量重建（不重跑 markitdown）→ 重建完成前旧集合继续服务 → 原子切换配置、下线旧集合（保留期可回滚）

## 多路召回与聚合审查（P3）

> **落地状态：设计定稿，代码未实现**（P3）——实现后更新根 `docs/architecture.md`「设计落地状态」。
>
> 召回总纲、关系建模与公开数据集标定方法见 [docs/social-recall.md](../../../docs/social-recall.md)；社交图服务见 [service/social-graph/docs/architecture.md](../../social-graph/docs/architecture.md)。
>
> 本节只定义 AI 服务的消费方式。所有融合权重均为配置项，文档只给序关系与来源，不固化数值。

### 处理顺序

```text
权限硬过滤
→ 向量召回 + 关键词召回
→ 合并去重 → 排名归一化或 RRF
→ 时效乘性衰减
→ star / 社交加性融合
→ 取 Top-K → Decima-small 相关性审查
→ 最终排序
```

权限过滤必须先于两条召回链路：私有库排除非成员，公开库允许所有人。未授权文档不得进入候选集，也不得发送给 Decima-small。

### 双路召回与基础融合

向量链路和关键词链路分别取 `Top-Nv` / `Top-Nk`，按文档块 ID 合并去重。两条链路的原始分数不直接相加，统一使用排名归一化或 RRF：

```text
V(d) = (Nv - vector_rank(d) + 1) / Nv
K(d) = (Nk - keyword_rank(d) + 1) / Nk
A(d) = 双路命中标记（命中为 1，否则 0）

线性融合：R(d) = w_v × V(d) + w_k × K(d) + w_both × A(d)
RRF：     R(d) = Σ 1 / (k + rank_i(d))
```

- 未被某条链路命中的文档，该链路得分为 `0`；
- `w_v` / `w_k` / `w_both` / `k` 均为配置或待标定超参；
- 社交分分布不均时优先用 RRF 兜底，避免量纲主导。

### 时效衰减（乘性）

时效是**乘性**项：过时文章语义再相关也应被压降，而不是被高语义分抵消。

```text
ageDays   = now - freshnessAt(article)
timeDecay = max(pow(0.5, ageDays / halfLife), decayFloor)
B(d)      = R(d) × timeDecay(d)
```

- `freshnessAt` 优先取 `articles.updated_at`（最后修改），回退 `created_at`（发布）；
- `halfLife` 是待标定超参；`decayFloor` 是结构性常数，防止经典老文章被完全压死；
- 当前没有内容级更新时间字段；如后续需要区分「正文更新」与「元数据编辑」，再补 `content_updated_at`，不在本阶段扩展。

### star 热度（加性）

项目 star 是**知识库级**：文章热度取所属知识库 `star_count`，不做文章级 star。

```text
starCount = kb.star_count
if 当前用户 star 过该 kb: starCount--        // 结构性处理，避免重复奖励
starScore = min(log1p(starCount) / log1p(p95StarCount), 1)
```

- `p95StarCount` 由离线统计知识库 star 分布得到，是统计参数，不是权重；需随数据定期刷新；
- `log1p` 压缩与 P95 封顶是结构性处理；
- 所有 star 数都为 0 时 `starScore` 取 0。

### 社交分数（加性）

社交分由 social-graph 服务提供原始信号，AI 服务负责映射与融合。对候选集调用 `BatchGetArticleSocialSignals`，只传已授权候选；响应带 `tier`、结构信号与 `profile_version`。

```text
tier        = socialGraph.tier   // 已按 KB/作者关系取最高档
socialScore = clamp(
    tierScore(tier)
  + pagerankBonus
  + communityBonus
  + jaccardBonus,
  0, 1
)
```

`tier` 由 social-graph 服务返回：`max(kbTier(kb, kb_owner), authorTier(author))`，覆盖 `own_kb` / `collaborator` / `starred_kb` / `follow(owner)` / `self_authored` / `mutual_follow` / `follow` / `starred_author` 等关系。

关系优先级只给序关系：

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

- 多信号命中时直接关系**取最高档**，不把档位分值相加；结构派生信号作为加成项；
- `same_community` / `jaccard` 已经是档位时不再重复加对应 bonus；更高档位是否叠加由实验决定；
- `starred_kb` 表示我 star 了该 KB（即关注该库）；`starred_author` 表示我 star 过该作者拥有的其他 KB，是弱作者聚合信号；
- `FOLLOWED_AUTHOR` 只由 `user_follows` 表达，不由 star 推导；
- PageRank、Leiden、Jaccard、二跳的系数均为待标定超参；Node2Vec 是并列对比项，不与图分数简单叠加；
- 社交图服务不可用或 profile 未命中时，`socialScore` 置 0 或回退旧五档规则（配置），不得绕过权限。

### Decima-small 相关性审查

对融合排序后的 Top-K 候选调用 Decima-small 的 `noul` 问题，判断「该文档是否直接有助于回答当前用户问题」，得到 `audit(d) ∈ [0,1]`：

```text
S_pre(d)   = B(d) + w_star × starScore(kb(d)) + w_social × socialScore(u, d)
S_final(d) = S_pre(d) + w_audit × audit(d)
```

- Decima-small 只提供相关性信号，不能绕过权限、替代两条召回链路或单独决定最终结果；
- `w_audit` 是独立配置项，与社交/star 权重分开标定；
- Top-K 的 `K` 是工程配置，不是权重。

### 候选元数据契约

AI 服务在向量写入/检索时需要拿到以下字段（不可变字段可随向量 metadata 写入，动态字段查询时从 Repository 或缓存取）：

| 字段 | 来源 | 用途 |
|---|---|---|
| `article_xid` / `kb_xid` | 向量 metadata | 候选标识、权限过滤 |
| `author_xid` | `articles.created_by_xid` | 作者关系、社交分 |
| `kb_owner_xid` | `knowledge_bases.owner_xid` | 库主关系、社交分 |
| `created_at` / `updated_at` | `articles` | 时效衰减 |
| `kb_star_count` | `knowledge_bases.star_count` | star 热度（动态，查询时取最新值） |

### 配置项（AI 服务持有）

| 配置 | 说明 | 来源 |
|---|---|---|
| `w_v` / `w_k` / `w_both` 或 `alpha` | 语义、关键词、双路命中的融合比例 | T2Ranking / DuReader_retrieval |
| `w_star` | star 热度融合权重 | 第一层联合校准 |
| `w_social` | 社交分数融合权重 | 第一层联合校准 |
| `w_audit` | Decima 审查分权重 | Top-K 复核实验 |
| `time_half_life` | 时效半衰期 | ChronoQA |
| `time_decay_floor` | 衰减下限（结构性常数） | 不参与调优 |
| `star_p95` | 知识库 star P95（统计参数） | 数据分布统计，定期刷新 |
| 关系档位分值 | 各档位的相对分值 | Dianping/Douban + 合成变体 |
| `pagerank_bonus` / `community_bonus` / `jaccard_bonus` | 结构加成系数 | 第二层消融 |
| `social_fallback_mode` | `zero` 或 `legacy_tier` | 工程配置 |
| `top_nv` / `top_nk` / `top_k_audit` | 候选规模 | 工程配置 |

### 可解释与消融

每个候选保留 `vector_rank`、`keyword_rank`、`V`、`K`、`timeDecay`、`starScore`、`socialScore` 的各分项、`audit`、`final_score`，支持：

- 解释排名：为什么这篇排前面；
- 网格调参/贝叶斯优化；
- 消融实验：去掉关键词、社交、时间、star 或 Decima 审查，分别评估 Recall@K、MRR、nDCG@K；
- 分社交密度报告稳定性，避免只在一个模拟分布上取最优。

### 边界

- 社交信号只作用于**已授权候选**；
- star/社交/时效只做排序，不影响权限；
- Decima 不直接决定数据库写入、工具调用等副作用操作；
- 公开数据集调出的权重只是上线初值，最终以真实日志/A/B 测试微调。
