# GanRAG v3 数据集与融合排序方案（冻结版）

> **状态**：设计冻结，待执行。  
> **冻结日期**：2026-11-08。  
> **适用范围**：GanRAG 社交感知召回融合的离线数据集构造、实验协议与论文结论边界。  
> **执行原则**：先冻结协议与 Schema，完成原生数据验收和小规模 L2，再启动全量物化。  
> **硬约束**：全本地、免费，不使用任何付费 API。

---

## 0. 一页结论

- v3 研究设计 **GO**，可以冻结 Schema、数据来源和评估协议。
- **不立即启动全量物化**。先完成 9 份核心文件 + `environment.lock`，再做原生数据验收和 100–200 query 的小规模 L2。
- 三类指标严格分表，不允许合并成一个总分：
  - **Original**：T2Ranking、DISGCN、ChronoQA 原生任务指标；
  - **Synthetic**：受控反事实实验的机制指标；
  - **Cross-task**：加入社交/star/时间后的语义保持与消融指标。
- 论文只论证：
  - 社交信号在原生推荐任务中的预测价值；
  - 不同关系特征对语义排序的机制响应、稳定性和参数可行域；
  - 不声称真实用户偏好权重或真实个性化效用。
- 主模型是**可解释分层融合公式**；LightGBM 放 Future Work。

---

## 1. 研究问题与贡献定位

### 1.1 研究问题

> 在固定语义排序质量的约束下，不同社交关系特征具有怎样的排序影响范围？这种影响是否稳定、可解释，并且能够通过独立关系特征与优先级阶梯进行合理表达？

不再使用以下表述：

> 利用公开数据集标定不同社交关系的真实权重，并证明社交能够提升真实用户检索效果。

### 1.2 贡献定位

采用 **A + C**：

1. **面向知识库 RAG 的社交感知特征建模框架**：解耦 visibility、ownership、social graph、collaboration、personal affinity、popularity、temporal。
2. **可解释、可复现的融合评估框架**：L1 原生任务校准 + L2 受控反事实实验 + L3 外部验证。
3. **可解释融合模型与参数可行域**：优先级阶梯、独立关系加性模型、少量交互项；输出可行区间，不输出单一“真实最优权重”。

不宣称新 SOTA，不宣称真实用户个性化增益。

---

## 2. 证据边界

| 证据类别 | 可以证明 | 不可以证明 |
|---|---|---|
| Original Benchmark | 原生任务上的预测能力 | GanRAG 真实用户效用 |
| Synthetic Mechanism | 算法响应、参数恢复、稳定性 | 真实社交偏好 |
| Cross-Dataset | 跨数据集趋势是否一致 | 跨业务领域必然有效 |
| Product Simulation | 设定条件下的产品排序行为 | 线上业务收益 |

所有实验结论必须标注所属类别；三类指标不得跨类聚合。

---

## 3. 数据源与三层结构

### 3.1 数据源

| 数据源 | 角色 | 当前状态 |
|---|---|---|
| T2Ranking | 语义检索骨架 | 已下载 collection/queries/qrels；passage-level |
| DISGCN Beibei | 社交推荐主实验 | 已下载 social/train/val/test/ufi |
| DISGCN Beidian | 跨数据集复现 | 已下载 social/train/val/test/ufi |
| ChronoQA | 时间约束与证据检索 | 已下载 chronoqa.jsonl + sina_news_original_data.csv |
| Douban / ZhihuRec / DuReader | 外部辅助 | P2，不主导 |

已核实：

- DISGCN Beibei `ufi`：714,111 行；Beidian `ufi`：4,004 行；格式为 `user_id / friend_id / item_id`。
- ChronoQA `sina_news_original_data.csv`：约 292,673 条新闻，含 `title/url/date/text`。
- ChronoQA `golden_chunks_urls`：7,414 个 URL，全部能在 CSV 中找到，`date` 均非空；覆盖率 100%。
- T2Ranking 当前只有 `collection.tsv`（pid/text）、queries、qrels；**没有 XML 文档资源，也没有 document_id 映射**。
- T2Ranking 有 `queries.test.tsv`，但**没有 `qrels.test.tsv`**；因此“没有可用的官方 test qrels”，不是“没有 test queries”。

### 3.2 三层结构

**L1 原生任务校准**

- 语义：T2Ranking passage-level，qrels 四级标签；
- 社交：DISGCN Beibei/Beidian，social + interaction + UFI；
- 时间：ChronoQA 证据检索 + 真实新闻发布时间代理实验。

**L2 受控联合实验**

- T2Ranking 语义骨架；
- 独立关系特征 + 派生 top_tier；
- visibility / ownership / social / collaboration / affinity / popularity 分离；
- judged negative + 未标注 hard candidate；
- 1,000 个完整 counterfactual group；
- train/val/test 按 query 隔离；
- 时间只做 synthetic mechanism test，不进入真实时间权重主实验。

**L3 外部验证**

- T2Ranking 官方 dev；
- DISGCN test；
- ChronoQA 独立时间任务；
- Beidian 跨数据集复现；
- 不参与调参。

### 3.3 数据规模

- L2：2k–5k query，20k–40k 基础 pair，180k–360k record，至少 1,000 个完整 counterfactual group；
- 每档独立 pair：2k–3k；
- 人工锚点：P0 300 条审计 / 120 条双人标注；P1 补到 450 / 150；
- 小规模 L2：100–200 query，先验收 Schema、split、共线性、人工锚点。

---

## 4. 实验矩阵

| 编号 | 内容 | 优先级 | 类别 |
|---|---|---|---|
| E01 | T2Ranking 语义检索 | P0 | Original |
| E01b | Reranker 语义对照 | P1 | Original |
| E02a | Beibei social + interaction | P0 | Original |
| E02b | Beibei social + UFI | P0 | Original |
| E03a | Beidian social + interaction | P1 | Cross-Dataset |
| E03b | Beidian social + UFI | P1 | Cross-Dataset |
| E04 | ChronoQA Evidence Retrieval | P0 | Original |
| E05a | Synthetic Social Mechanism | P0 | Synthetic |
| E05b | Robustness Test | P0 | Synthetic |
| E06 | Priority vs Additive | P0 | Synthetic |
| E07 | Graph Randomization | P1 | Synthetic |
| E08 | Parameter Recovery / Robustness | P0 | Synthetic |
| E09 | ChronoQA Full Article Temporal Retrieval | P0 | Original Proxy |
| E10a | Semantic Preservation | P0 | Cross-task |
| E10b | Social Sensitivity | P0 | Synthetic |
| E10c | Parameter Stability | P0 | Synthetic |

### 4.1 E05/E06 反事实实验边界

- **E05a Mechanism Test**：给定已知生成权重，只改变一个关系特征，验证融合公式是否按设定机制响应。
- **E05b Robustness Test**：改变关系密度、重叠比例、图结构、噪声、随机种子，验证排序响应稳定性。
- **E06 Priority vs Additive**：同一批 counterfactual group、同一冻结语义分，比较优先级阶梯与独立关系加性模型。
- `Treatment Recovery Error` 只表示“设定参数恢复误差”，不得写成真实权重恢复误差。

### 4.2 E09 时间代理实验

- E09a：完整新闻文章级证据召回 + Unjudged@K；
- E09b：golden chunk 级证据召回；
- E09c：完整新闻文章 + 时间过滤后的已知证据召回；
- 非 golden article 标记 Unjudged，不直接作为负例，不计算完整 NDCG；
- E09 只作为“真实新闻发布时间的时间约束检索代理实验”，不推导知识库文章通用 freshness 半衰期。

### 4.3 E10 语义保护与参数稳定性

- E10a Semantic Preservation：NDCG@10、Recall@20、Spearman Rank Correlation、Top-K Overlap；
- E10b Social Sensitivity：每种关系单独加入时的 Top-K 变化、贡献占比、无关系用户排序保持；
- E10c Parameter Stability：可行域、随机种子稳定性、社交密度稳定性、语义分差稳定性、ε 敏感性；
- 增加 Semantic Margin Sensitivity：研究 `Δ_semantic` 与 `S_social` 的相对关系，输出“社交分需要多大才能改变语义排名”的曲线。

---

## 5. Schema 与特征字典

### 5.1 粒度

- `passage_id`：T2Ranking 原生 passage；
- `source_document_id`：仅当原始文档资源可可靠恢复时填写（当前 P0 不填）；
- `synthetic_article_id`：产品语义别名，不声称是原始文档；
- `synthetic_kb_id` / `synthetic_author_id`：合成映射；
- `article_author_mapping_source = synthetic`；
- `kb_mapping_source = synthetic`；
- `item_granularity: passage | document | synthetic_article | news_article`。

### 5.2 关键字段

```text
sample_id
base_pair_id
counterfactual_group_id
query_id
article_id
user_id
split
item_granularity

visibility: public | private | member_only
ownership:
  self_authored
  own_kb

social_graph:
  mutual_follow
  one_way_follow
  two_hop_only
  follows_author
  author_follows_user
  two_hop_path_count

collaboration:
  collaborator
  collaboration_role

personal_affinity:
  user_starred_kb
  author_star_signal

popularity:
  kb_global_star_count
  kb_global_star_count_excluding_user
  snapshot_at
  is_synthetic

temporal:
  published_at
  updated_at
  created_at
  event_time
  effective_freshness_at
  freshness_time_source
  freshness_time_confidence
  query_time
  temporal_intent

labels:
  semantic_relevance
  label_source
  label_status: positive | judged_negative | unjudged
  negative_subtype: random_judged | bm25_hard_judged |
                   embedding_hard_judged | dual_hard_judged | null
  evidence_validity: necessary | supportive | irrelevant | uncertain

semantic:
  semantic_score
  semantic_score_config_id

synthetic:
  is_synthetic
  is_synthetic_time
  source_dataset
  source_record_id
  generation_seed
  top_tier
```

### 5.3 关系规则

- `mutual_follow`：双向关注；
- `one_way_follow`：只有单向关注；
- `two_hop_only`：最短路径距离恰好为 2，排除一跳；
- `collaborator`：排除 KB owner；
- `starred_author`：排除当前候选 KB，避免同一 Star 行为双重计分；
- `no_social_relation`：所有社交项严格为 0；
- `visibility` 只做硬过滤，不参与打分；
- `ownership` 独立特征，不自动等于高相关；
- `top_tier` 只用于产品兼容基线和消融。

### 5.4 语义分数冻结

```text
S_semantic = RRF(BM25_top100, Qwen3-Embedding_top100, k=60)
S_semantic_norm = S_semantic / (2 / (k + 1))   # [0,1]
semantic_score_config_id = sem_v3_rrf60
```

规则：

- 候选集固定为 BM25 Top-100 ∪ Embedding Top-100；
- 未进入某检索器 Top-100：该检索器贡献为 0；
- 检索器失败 / 超时 / 索引不可用：标记 `retrieval_failure`，不得当作正常缺失；
- 不补最大排名；
- 名称统一为“归一化 RRF 分数”，不称为相关性概率；
- Reranker 只做 E01b 对照，不进入主融合；
- 冻结 BM25 tokenizer、Embedding 模型版本、归一化方式、Top-K、缺失排名处理、候选生成逻辑。

### 5.5 特征标准化

| 特征 | 标准化 |
|---|---|
| semantic | 归一化 RRF，[0,1] |
| ownership / social / collaboration / affinity | {0,1} |
| popularity | `log1p(star_count) / log1p(P95(star_count))`，clip [0,1] |
| temporal decay | [0,1] |

- 不做统一 z-score；
- `P95(star_count)` 只从训练集或预先冻结的参考统计集计算；
- `P95 = 0` 时 popularity 统一为 0；
- 缺失社交关系不得默认转成负关系；
- 正常无关系用户的社交分严格为 0；
- popularity 必须有 `snapshot_at`，不得使用查询时间之后的 star 数。

---

## 6. 数据构造规则

### 6.1 T2Ranking

- P0 只做 passage-level；
- 不声称 article/document 级作者、时间；
- 20k query 子集 + 官方 dev；
- qrels rel>=1 为 Positive，rel=0 为 Judged Negative，不在 qrels 中为 Unjudged；
- 有 `queries.test.tsv`，但没有 `qrels.test.tsv`，不使用官方 test qrels；
- split 按 query 分组；近重复 query、passage 重复/近重复单独检查；
- 同一 passage 被不同 query 复用时，必须记录并做泄漏审计。

### 6.2 DISGCN

原生图特征：

- `neighbor_exists`
- `reciprocal_edge`
- `shortest_distance`
- `influence_triplet`
- `interaction_history`

合成业务特征：

- `mutual_follow`
- `one_way_follow`
- `two_hop_only`
- `collaborator`
- `starred_kb`
- `starred_author`

规则：

- `reciprocal_edge` 不等于 `mutual_follow`，中间必须有合成映射声明；
- 没有原生数据支持的特征，只能在 L2 中作为合成业务特征研究，不能声称在 DISGCN 中得到验证；
- UFI 只使用训练期可见信息，绝不用 test 交互构造训练特征；
- Beibei → Beidian 是跨平台、跨用户、跨商品的 Cross-Dataset Replication，不共享用户 ID、商品 ID、嵌入；
- 分别报告，不拼接训练。

### 6.3 ChronoQA

数据结构：

```text
question_id
evidence_group_id
evidence_document_id
evidence_passage_id
is_required_evidence
published_at
published_at_source
published_at_confidence
url_date
date_conflict
```

规则：

- `published_at` 来自 `sina_news_original_data.csv` 的 `date`；
- 第一轮随机抽 300 条检查整体日期映射；
- 第二轮异常分层抽 100 条，重点检查跨年、日期冲突、同 URL 多记录、缺正文、模糊时间；
- CSV date 与 URL 日期一致：进入严格时间实验；
- CSV date 有值、URL 无日期：保留 CSV 日期并标记来源；
- CSV date 与 URL 日期冲突：标记 conflict，人工复核；
- 无法确认真实发布时间：`time_confidence=low`，排除严格时间实验；
- CSV date 明确是抓取时间而非发布时间：不得作为发布时间；
- 非 golden article 标记 Unjudged，不直接当负例；
- 多证据问题用 `EvidenceRecall@K` 和 `CompleteEvidenceRecall@K`；
- `evidence_validity` 主指标只用 `necessary`，次指标报告 `necessary + supportive`。

### 6.4 负例

- Positive：qrels rel=1/2/3；
- Judged Negative：qrels rel=0；
- Hard Negative：Judged Negative 中 BM25/Embedding 排名靠前的子集；
- Unjudged：不在 qrels 中，只做召回覆盖分析，不参与正式指标；
- P0 不设固定 `neg_ratio`，不使用 PU Learning；
- query 样本不足时标记 `insufficient_judgments`；
- 两套评估：
  - Official Judged Evaluation：只用 qrels 已标注 pair；
  - Retrieval Coverage Evaluation：真实 BM25/Embedding 候选，报告已知正例 Recall、Unjudged@20、Judged@20；
- 人工补标高排名 Unjudged 候选作为 P1，形成独立 `human_judged_extension.jsonl`，不混入官方 qrels。

### 6.5 防泄漏

- query 分组；
- 近重复 query 不跨 split；
- 同一 `counterfactual_group_id` 不跨 split；
- passage/document 重复与近重复单独检查；
- 同一原始文档切出的多个 passage 不得跨 split，或明确标记为允许共享；
- 合成用户及其关系结构的跨 split 复用必须记录；
- 明确哪些实验采用用户留出；
- 只做 query 分组不能自动排除 passage 复用造成的信息泄漏。

### 6.6 人工锚点与 LLM 审计

- P0：300 条总审计、120 条双人标注；
- P1：补到 450 / 150；
- 抽样采用分层 + 难例过采样：
  - Semantic：rel=0、rel=1、边界样本；
  - Social：多关系重叠、方向冲突、二跳边界；
  - Temporal：URL 日期异常、跨年、相对时间、多证据；
- Semantic 用 Quadratic Weighted Kappa；
- Social / Temporal 用 Cohen's Kappa + Raw Agreement；
- 一致性系数报告置信区间；
- 确定性规则先用自动化断言验证；
- LLM 只做固定 300 条自然度审计，不参与选参、不生成相关性标签、不生成负例标签；
- 固定 prompt、模型版本、temperature，保存原始响应；
- P0 双人标注后，如果某一类别一致性明显偏低：先修正定义、重新抽样验证，再进入全量物化。

---

## 7. 融合模型

### 7.1 分层公式

第一层：权限硬过滤

```text
visibility = public | private | member_only
权限在召回和排序之前执行，不参与打分。
```

第二层：语义基础分

```text
S_1 = S_semantic_norm × D(age, intent)
```

第三层：辅助信号

```text
S_final = S_1
        + w_own × ownership
        + Σ_r w_r × social_graph_r
        + w_collab × collaboration
        + w_aff × personal_affinity
        + w_pop × popularity
```

约束：

- `no_social_relation` 时所有社交项为 0；
- `semantic` 权重固定 1.0；
- 辅助权重主实验范围 [0,1]，步长 0.05；
- 诊断实验单权重扩展到 [0,2]，不参与主参数选择；
- 不设辅助总分硬上限，但报告 `aux_total` 和 `R_aux = aux_total / S_semantic`；
- `S_semantic = 0` 时 `R_aux = null`；
- 记录 `boundary_hit_ratio`：可行参数中落在搜索边界上的比例。

### 7.2 时间衰减作用范围

主实验：

```text
S_final = S_semantic × D(age, intent) + S_aux
```

P1 对照：

```text
S_whole = (S_semantic + S_aux) × D(age, intent)
```

说明：

- 主实验 `semantic_only` 与产品“时效乘性衰减语义、社交/star 加性融合”口径一致；
- `whole_score` 是对照设计，不是天然更合理；
- 不因为 `whole_score` 降低旧文章排名就认定它更好；
- E10 观察旧文章是否因社交信号重新进入 Top-K，以及两种策略的语义保持差异。

### 7.3 意图感知时间处理

| temporal_intent | 处理 |
|---|---|
| unconstrained | D = 1，不衰减 |
| absolute_time | 硬过滤 |
| relative_time | 按时间窗过滤 |
| freshness | 软时间衰减 |
| historical | 按 event_time / 历史语义处理，不用普通 freshness 惩罚 |

### 7.4 共线性与参数可行域

- 计算 Pearson / Spearman / VIF；
- VIF > 5 标记高共线；
- VIF 不自动删除特征；
- 冻结 `features.yaml` 前，用 100–200 query 小规模 L2 跑共线性诊断；
- 只加入少量预先指定的 interaction terms；
- 不为降低 VIF 机械删除有研究意义的特征；
- 高共线特征报告“独立影响难以辨识”，组合影响作为补充分析；
- 最终输出参数可行域，不输出单一最优权重。

### 7.5 语义质量约束

- 使用绝对差值，不用相对损失；
- 同时约束 NDCG@10 与 Recall@20；
- 预注册三档：ε = 0.005 / 0.010 / 0.020；
- 使用配对 Bootstrap 的 95% CI 下界判断非劣效：
  - `CI_lower(ΔNDCG@10) >= -ε`
  - `CI_lower(ΔRecall@20) >= -ε`
- 三档 ε 都报告；
- 不把 0.01 说成已证明的业务容忍度。

---

## 8. 评估协议

### 8.1 指标分类

**Original**

- T2Ranking：NDCG@10、Recall@20、MRR@10、Judged Recall@20；
- DISGCN：NDCG@K、HR@K、Recall@K；
- ChronoQA：EvidenceRecall@K、CompleteEvidenceRecall@K、Temporal Accuracy、Constraint Violation Rate。

**Synthetic**

- Pairwise Accuracy；
- Treatment Recovery Error（设定参数恢复误差）；
- Rank Consistency；
- Parameter Response Variance；
- Top-K Change Rate。

**Cross-task**

- 语义 NDCG/Recall 变化；
- Spearman Rank Correlation；
- Top-K Overlap；
- Semantic Margin Sensitivity；
- 参数可行域稳定性。

三类指标不得合并成一个总分。

### 8.2 Judged Recall@20 定义

```text
Judged Recall@20 =
Top-20 中检索到的已标注正例数
/
该 query 的全部已标注正例数
```

同时报告：

- `Judged@20`：Top-20 中已标注样本数；
- `Unjudged@20`：Top-20 中未标注样本数；
- `Known Positive Coverage@20`：已知正例的候选召回覆盖。

Judged Recall 只衡量已知正例召回，不是完整语料的真实 Recall。

### 8.3 统计

- query 聚类 Bootstrap 2,000 次；
- 5 个随机种子；
- 模型比较用同一批 query 的配对 Bootstrap；
- 跨用户实验用 query × user 两级聚类；
- 多模型比较用 Holm-Bonferroni；
- 报告 absolute delta、relative improvement、95% CI of paired delta、p-value；
- 不只报 p-value，也不只看两个 CI 是否重叠；
- 最终测试集只用于最终报告。

### 8.4 E09 评估规则

- 完整 292k 新闻语料只用于候选召回、Retrieval Coverage、Unjudged@K、已知证据可召回性；
- 有证据标注的 golden article / chunk 用于 EvidenceRecall@K、CompleteEvidenceRecall@K、时间过滤前后已知证据召回变化；
- 非 golden article 标记 Unjudged，不计算完整 NDCG；
- E09 不报告“完整新闻语料上的真实排序 NDCG”；
- 时间过滤前后的比较只解释为“已知证据召回变化”。

### 8.5 E10 评估规则

- E10a Semantic Preservation：NDCG@10、Recall@20、Spearman Rank Correlation、Top-K Overlap；
- E10b Social Sensitivity：每种关系单独加入时的 Top-K 变化、贡献占比、无关系用户排序保持；
- E10c Parameter Stability：可行域、随机种子稳定性、社交密度稳定性、语义分差稳定性、ε 敏感性；
- Semantic Margin Sensitivity：输出“社交分需要多大才能改变语义排名”的曲线。

---

## 9. 许可证与 provenance

### 9.1 licenses.md 冻结内容

```text
t2ranking:
  repository_license: Apache-2.0  # 待核实当前仓库 LICENSE
  dataset_license: unverified
  auxiliary_resource_license: unverified
  redistribution_allowed: false   # 核实前默认不公开再分发

chronoqa:
  license: CC-BY-4.0
  redistribution_allowed: true    # 保留署名与许可声明

disgcn:
  repository_license: unverified
  dataset_license: unverified
  redistribution_allowed: false
  note: 公开可下载不等于允许任意再分发；仓库、数据文件、论文分别核查

derived_files:
  license_status: unverified
  note: 衍生数据再分发前单独审查，不能继承原始仓库代码许可证
```

论文发布前必须完成：

- T2Ranking 数据集文件的实际许可核实；
- DISGCN 数据文件的实际许可核实；
- 衍生文件是否包含不能再分发的原始内容；
- 不能再分发的文件不进入公开衍生包。

### 9.2 provenance.yaml

记录：

- 仓库、论文、固定 commit / DOI；
- 下载日期；
- 每个原始文件 SHA256；
- 许可证状态；
- 是否允许再分发。

---

## 10. 冻结文件清单与内容

9 份核心文件 + `environment.lock`：

1. `dataset.yaml`：数据源版本、文件清单、split、样本规模、合成标记、许可证。
2. `features.yaml`：字段名、类型、来源、是否合成、标准化方式、允许参与实验、逻辑互斥/重叠规则、RRF 候选生成、VIF 与 interaction terms 冻结规则。
3. `schema.md`：粒度、字段、`counterfactual_group_id`、visibility/ownership/social/collaboration/affinity/popularity/temporal、`top_tier`。
4. `ground_truth_sources.md`：每个标签来源、原始任务、可信度、允许用于哪些指标、`label_status` / `negative_subtype`。
5. `licenses.md`：按 §9.1 冻结。
6. `dataset_card.md`：来源、原始任务、合成组件、Ground Truth、合成标签、split、泄漏防护、已知偏差、Intended / Not Intended Use、Limitations。
7. `provenance.yaml`：按 §9.2 冻结。
8. `evaluation.yaml`：Original / Synthetic / Cross-task 指标、E01–E10 主次指标、RRF 规则、Judged Recall 定义、E09 规则、ε、Bootstrap、多重检验。
9. `experiment_protocol.md`：研究假设、主次指标、对照组、参数范围、ε、Bootstrap、种子、排除/停止条件、多重检验、`time_decay_scope`、E09 口径、人工锚点修正流程。
10. `environment.lock`：Python 完整版本、依赖精确版本及哈希、CUDA/驱动/PyTorch、Embedding 模型 revision/digest、Ollama tag/digest、BM25 tokenizer/索引配置、关键随机种子。

---

## 11. P0 执行顺序与质量门槛

1. 冻结 9 份核心文件 + `environment.lock`；
2. 固定原始数据版本、SHA256、许可证状态；
3. 完成 E01、E02a/b、E04、E09 原生数据验收；
4. 生成 100–200 query 小规模 L2；
5. 跑逻辑断言、共线性诊断、300/120 人工锚点验收；
6. 冻结 interaction terms、参数范围、实验脚本；
7. 通过全部质量门槛后，启动全量物化；
8. 全量物化后执行 E01–E10，最终测试集只用于最终报告。

全量物化前必须通过：

- 小规模 L2 的 Schema 与 split 验收；
- 共线性诊断；
- 人工锚点一致性检查；
- E09 日期冲突与证据有效性验收。

---

## 12. 已知限制（Dataset Card / Limitations）

1. 社交关系图来自电商场景，与知识库协作关系存在领域差异。
2. KB Star、文章作者关系及成员协作关系部分由规则映射或合成生成。
3. 合成关系分数不代表真实用户的主观偏好强度。
4. 不存在真实 GanRAG 用户行为日志，无法验证实际用户满意度。
5. 时间数据集主要来自新闻语料，不完全等价于通用知识库新鲜度排序。
6. T2Ranking 是 passage-level 数据，不声称 document/article 级真实归属。
7. DISGCN 与 GanRAG 关系语义不存在严格一一映射。
8. 外部数据集在用户行为、内容分布及交互机制上存在差异。
9. 论文只论证原生任务预测价值、受控机制响应和参数可行域，不宣称真实用户效用。

---

## 13. 版本变更规则

冻结意味着锁定研究假设、字段定义和评价协议；后续发现数据错误可以修正，但任何实质性修改必须记录：

- 版本号；
- 修改原因；
- 影响范围；
- 是否影响既有实验结论；
- 是否需要重跑。

v2 保留作 baseline，不覆盖；v3 使用 `config_version=v3` 自动隔离到 `processed/v3`、`logs/runs.v3.jsonl`、`cache/v3`。

---

## 14. 新会话执行提示

后续在新会话中执行时，先读本文件，并按以下顺序开始：

1. 先落盘第 10 节的 9 份核心文件 + `environment.lock`；
2. 不要直接启动全量物化；
3. 先完成第 11 节的阶段 1–2；
4. 小规模 L2、共线性诊断、人工锚点通过后，再进入全量物化；
5. 任何架构层变更都要先更新本文件并记录版本。

本文件是 v3 的设计冻结稿，不是实现脚本；具体脚本、配置和数据产物按第 10–11 节执行。
