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

- **写入**：文章保存/删除的增量同步后，对 `articles.content` 分块、用当前模型向量化，写入 `kb-<kb_xid>-<model_id>` 集合（每条向量 metadata 记 `embedding_model`）
- **检索路由**：查询向量用当前模型编码，**只查当前模型后缀的集合**——用什么模型，就检索对应的向量化结果，切换模型不影响旧向量的可用性
- **切换模型**：新写入切新模型集合 → 按 content 全量重建（不重跑 markitdown）→ 重建完成前旧集合继续服务 → 原子切换配置、下线旧集合（保留期可回滚）

## 多路召回与聚合审查（P3）

> **落地状态：设计定稿，代码未实现**（P3）——实现后更新根 `docs/architecture.md`「设计落地状态」。

### 处理顺序

```text
权限硬过滤 → 向量召回 + 关键词召回 → 合并去重 → 基础排序
→ Decima-small 相关性审查 → 最终排序
```

权限过滤必须先于两条召回链路：私有库排除非成员，协作库只允许协作者，公开库允许所有人。未授权文档不得进入候选集，也不得发送给 Decima-small。

向量链路和关键词链路分别取候选，例如 `Nv = 100`、`Nk = 100`，按文档块 ID 合并去重。两条链路的原始分数不直接相加，统一使用排名归一化：

```text
V(d) = (Nv - vector_rank(d) + 1) / Nv
K(d) = (Nk - keyword_rank(d) + 1) / Nk
```

未被某条链路命中的文档，该链路得分为 `0`。双路命中信号为 `A(d)`（双路命中为 1，否则为 0），召回基础分为：

```text
R(d) = 0.55 × V(d) + 0.30 × K(d) + 0.15 × A(d)
```

关系、时间和热度只用于召回后的排序，不得影响权限判断：

```text
S_base(d | u, q) =
    0.55 × R(d)
  + 0.25 × rel(u, d)
  + 0.12 × time(u, d)
  + 0.08 × heat(d)
```

其中：

- `rel`：`OWN=1.0`、`MEMBER=0.8`、`STARRED=0.6`、`FOLLOWED_AUTHOR=0.4`、`OTHER=0.1`；多个关系同时存在时取最大值；
- `time = exp(-λ × Δdays)`；当前没有用户访问时间字段时，使用文章、知识库、star 或 follow 的最近有效时间；
- `heat = log(1 + star_count) / log(1 + max_star_count)`；所有 star 数都为 0 时取 0，使用对数平滑防止热门知识库无限霸榜。

对基础排序前 `K` 个候选（初始建议 `K=20`）调用 Decima-small 的 `noul` 问题，判断“该文档是否直接有助于回答当前用户问题”，得到 `audit(d) ∈ [0,1]`。最终分数为：

```text
S_final(d) = 0.85 × S_base(d) + 0.15 × audit(d)
```

Decima-small 只能提供相关性信号，不能绕过权限、替代两条召回链路或单独决定最终结果。每个候选保留 `vector_rank`、`keyword_rank`、各信号分数和 `final_score`，支持解释排名、网格调参与消融实验（去掉关键词、关系、时间、热度或 Decima 审查分别评估 Recall@K、MRR、nDCG@K）。
