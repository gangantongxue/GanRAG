# fix — 缺陷与修复记录

> 用途与使用规则见 [AGENTS.md](../AGENTS.md)「待完成事项管理」。一条记录一行、一个事项一条；完成只改状态列，**禁止删除记录**。
> 状态取值：`⬜待处理` / `🔨进行中` / `✅已完成(YYYY-MM-DD)`。编号 `FIX-NNN` 递增、不复用、不重排。
> 收录范围：缺陷、bug、与规则/文档不符的实现、需要修复性迁移的变更。

| ID | 记录 | 登记日期 | 状态 |
|----|------|---------|------|
| FIX-001 | `refresh_tokens` 含物理外键 `fk_refresh_tokens_user`，违反「禁止物理外键」规则 → 随 2026-10-15 迁移基线重写消除（数据盘已清、旧迁移全删重写，新迁移无 `refresh_tokens`、全部无外键） | 2026-10-15 | ✅已完成(2026-10-15) |
| FIX-002 | `task db-gen` 报错「迁移目录 ganrag_ai 未在 pkg/db/gen 的 schemata 中登记表清单」：2026-10-15 交付 `ganrag_ai`/`ganrag_repository` 迁移后未登记 schemata，db-gen 全程不可用，违反「表结构变更后必须 task db-gen」规则 | 2026-10-17 | ✅已完成(2026-10-17) |
| FIX-003 | 跨服务逻辑引用存的是目标自增 ID（repository `owner_id`/`created_by`、ai `user_id`/`kb_id`），下游服务拿不到 UserService/Repository 的自增 ID，无法解析归属 → 统一口径：同服务引用保留自增 ID 并冗余 `*_xid`，跨服务引用只存目标 xid；对象引用同时冗余 `content_object_xid`/`object_xid`；迁移 user 0005 / repository 0008 / ai 0005（当前全表为空） | 2026-10-18 | ✅已完成(2026-10-18) |
| FIX-004 | Garage 删除失败只写日志，无法集中重试与后台展示 → 新增公共 schema `ganrag_common.async_tasks` 与共享包 `pkg/asynctask`，业务事务内同事务写 outbox，成功/失败回写状态；其他服务直接 import 包使用，不新增独立服务 | 2026-10-18 | ✅已完成(2026-10-18) |
| FIX-005 | AI 服务 P3 召回公式与 product 检索权重表硬编码具体数值，且时效按加性接入，与「权重只给序关系、数值由实验标定」「时效乘性」决策冲突 → 文档改为符号化配置 + 乘性时效，统一指向 [social-recall.md](social-recall.md) | 2026-10-20 | ✅已完成(2026-10-20) |
| FIX-006 | 输入决策中的「文章 star / 文章协作者」与项目现有「知识库级 star + KB 成员」模型不一致 → 统一为知识库 star（文章继承）、KB 成员即协作者、`articles.created_by_xid` 为文章作者；「关注知识库」仅表达为 star 该库，不新增 follow-kb 表 | 2026-10-20 | ✅已完成(2026-10-20) |
| FIX-007 | `product.md`「关系状态」表仍引用已合并的 `kb_members` / `kb_stars`，与实际 `kb_user_relations` 不一致 → 改为 `kb_user_relations.role >= 1` / `starred = 1`，并补充「star 即关注知识库」口径 | 2026-10-20 | ✅已完成(2026-10-20) |
| FIX-008 | `docs/architecture.md` 中指向 `service/`、`pkg/` 的相对链接多写了一层 `../`（`../../service/...`），从 `docs/` 解析会跳出仓库 → 统一改为 `../service/...`、`../pkg/...`，并纳入文档链接检查 | 2026-10-20 | ✅已完成(2026-10-20) |
| FIX-009 | `service/ai/docs/architecture.md` 向量化章节、`docs/todo.md` 文档上传条目、`docs/product.md` 文章条仍写 `articles.content` / 「内容存 MySQL」，与实际「正文在 Garage、MySQL 只存 `content_key`」口径不一致 → 统一改为按 `articles.content_key` 读取/存储 md | 2026-10-20 | ✅已完成(2026-10-20) |
| FIX-010 | 融合脚本互关档位判定方向写错（`author in follows[u]` 与取池条件同义、反向边未验），产出的 `mutual_follow` 记录可为单向关注 → 改为严格双向边 + 双向佐证判定，`--check` 增档位方向回归断言，真数据 20,000 条 0 违规 | 2026-10-23 | ✅已完成(2026-10-23) |
| FIX-011 | ChronoQA `question_date` 存在非补零（`2024-6-5`）、中文（`2024年6月5日`）、乱码三种畸形，`date.fromisoformat` 直接抛异常致整轮 failed → 加载时机械归一化，解析不出置 None 计入 `time_missing_rate` | 2026-10-23 | ✅已完成(2026-10-23) |
| FIX-012 | 负例兜底对全局池做每 qid 全量扫描（20k qid × 百万池），全规模加载 5 分钟超时不可用 → 按 split 分桶 + 每 qid 稳定哈希起点环形遍历（O(want)），加载 77s / 构建 19s | 2026-10-23 | ✅已完成(2026-10-23) |
| FIX-013 | `RunsLog.append` 只写文件不同步内存记录，同进程第二轮读到过期状态致轮次重复编号、同 run_id 互覆、累计数归零 → append 同步内存；smoke 验证轮改写 `logs/smoke.jsonl` 与实验日志隔离 | 2026-10-23 | ✅已完成(2026-10-23) |
| FIX-014 | 批量抽查一条回答畸形即整批 8 条作废（8~24 条/轮被丢弃、样本量缩水致比率有偏），且重试固定 temperature=0 确定性重复同一坏输出形同虚设，第 8 轮累计丢 26.7% 触发 fail-loud 中止 → 整批失败机械降级逐条判定（契约不变）、重试升温换种子、失败原始回答留痕 findings.json | 2026-10-23 | ✅已完成(2026-10-23) |
| FIX-015 | `run_id` 不含计划槽：规则移动把不同槽收敛到同一参数 → 同 run_id 记录互覆（38 条仅 8 个存活、探索状态丢失）→ 计划反复选中已探索槽死循环（轮 18~37 约 2.5h 重复评同一配置、"累计完成"永不增长）；且去重「末条生效」让 interrupted 记录遮住 completed 致最优选择失真 → run_id 加计划槽哈希、探索状态按全量 completed 记录的 base_params 恢复、去重改 completed 优先 | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-016 | 融合调参规则表 R1~R4 依赖绝对阈值，在不完美真实标签上永远触发（实测 R1/R4 37/37 轮、R2 33/37 轮）：R1 每轮把 `neg_ratio` 降一档、R2/R4 对 `relation_strict` 一升一降净抵消，导致 26/37 轮实际执行参数 ≠ 计划槽（网格污染）、`neg_ratio=8` 与 `relation_strict=0.7` 从未真正测到 → 清空 `adjust_rules`（网格已全覆盖四参数、规则冗余有害），`apply_rules` 机制保留，自检改为空表断言 + 合成规则表机制回归 | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-017 | `self_authored` 永不产出：`iter_records` 在档位循环前 `used={au}`，`self_authored` 池只有 `[au]`，被 `x not in used` 过滤；标准档覆盖 5/6、full 8/9，`is_author` 恒 false → v2 改 `used=set()`，档位循环按产品优先级维护 `blocked`，自检覆盖 `self_authored` 与 `is_author` | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-018 | `public` 档位不纯：只排除证据达标的 follow/mutual 与 top-500 two_hop，弱证据直系关注者、500 名以后二跳仍可能被标 public；反之弱证据直系关注者可能进入 `two_hop` → v2 排除全部直接关注/粉丝、KB 成员、star、二跳候选，`two_hop` 同样排除全部直接边，并在 `check_record` 机械校验 | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-019 | 档位优先级未落地：`social-recall.md` 4.5 要求多信号命中取最高档，实际每档独立取样，`used` 只保证同记录不重复；collaborator 同时 follow 作者等会被标成低档 → v2 按 `self/own_kb > mutual > follow > collaborator > starred_kb > two_hop > starred_author > public` 维护 `blocked`，高优先级池内的用户不再进入低档 | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-020 | `relation_strict` 档位塌缩：0.4/0.5/0.6 经 `round(x*4)-1` 都映射到 `mev=1`，fine 搜索的 0.4/0.6 是 no-op → v2 改为 0.3/0.4/0.5/0.6/0.7 → 0/1/2/3/4 的单调映射，保证每档产生不同社交池 | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-021 | `time_fit` 把未来发布（`published_at > question_date`）也算作 stale，时间语义错误 → v2 增加 `not_yet_published`，未来发布单独标记并进可疑池，不再混入 stale | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-022 | Schema 语义错误：`kb_role` 对 public/starred 等非 KB 成员也填 `member`；`star_count` 实际是文章 KB 的 star 数；`is_author` 因 FIX-017 恒 false；`social_features.jaccard/pagerank/leiden` 恒为 null 但 README 宣称存在 → v2 非成员 `kb_role=null`、字段改名 `kb_star_count`、修正 `is_author`，移除三个空复杂图特征并改文档 | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-023 | 过程记录不可用：`runs.jsonl` 92% 字节是内嵌 `parse_failures` 原文（单条平均 24KB），违背「大产物只存路径引用」；版本门只查 `config_version`，FIX-016 改配置未 bump 版本仍可通过 → v2 只留计数/路径，`config_fingerprint` 纳入 run_id 与版本门，输出按版本隔离 | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-024 | Ollama 抽查上下文截断：批量 8 条 × 600 字符未设 `num_ctx`，响应常在 360–750 字符处出现半截 JSON；主路径 `temperature=0.7` 时重试 `max(0.7,0.6)` 实际没有升温 → v2 设 `num_ctx=8192`、检查 `done_reason`、重试阶梯升温，并保留逐条降级链 | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-025 | 融合输出未按版本隔离：`logs/runs.jsonl`、`processed/rounds/`、`processed/candidates.*.jsonl` 混用固定路径，v2 与 v1 无法并存；v1 的 111 个 artifact 引用已全部失效 → v2 全部路径由 `config_version` 派生（`logs/runs.v2.jsonl`、`processed/v2/...`、`cache/v2/...`），v1 核心产物归档到 `archive/v1/`，v3 自动写新文件无需迁移 | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-026 | QC 抽样噪声主导：每轮仅 120 条（README 写 500–1000），代表池固定每层 12 条、重复轮次只从固定池换抽，26 次同参数复测 `qc_score=0.8067±0.0149`，best 0.8398 不可区分；`time_ok` 随 `time_inject_ratio` 反向变化、coverage 被 FIX-017 卡死 → v2 改 200 代表 + 60 可疑 × 3 次采样，QC 分数加 bootstrap CI 并从 coverage/扣分改为 gate，选参改用 NDCG | 2026-10-24 | ✅已完成(2026-10-24) |
| FIX-027 | 登录与用户数据模型未为端到端加密预留：现役注册/登录把明文密码交给服务端并以 bcrypt `users.password_hash` 校验，与 OPAQUE/aPAKE「服务端不见密码」冲突；聊天 E2EE 所需的长期身份私钥没有托管/恢复位置 → 新增 [e2ee.md](e2ee.md) 定稿（OPAQUE RFC 9807 + `export_key` 加密的 key bundle + `user_e2ee` 表 + 多轮登录 + 恢复码重置；恢复码保证重置密码后仍可查看历史消息），登录/表结构改造待实现（见 todo）；威胁模型 A 下弱密码离线猜测为已知残余风险；迁移策略待实现时确认 | 2026-11-01 | 🔨进行中 |
| FIX-028 | 召回文档仍把 Decima-small 作为最终融合后的 Top-K `noul` 相关性审查并保留 `w_audit` 加性项，与最新决策「语义重排改由专用重排模型承担、禁止最终融合后全局重排、最终排序由融合公式决定」不符 → `w_audit` 退出主公式；Decima `noul` 降级为语义路可选排序器（默认关闭）；重排模型采用 `Qwen3-Reranker-0.6B`（8GB 显存可在线，4B 不采用）；同步 social-recall / ai / product / data-model / vector-store / social-graph / architecture / README / datasets | 2026-11-02 | ✅已完成(2026-11-02) |
| FIX-029 | 数据集章节仍写「整体调参、不记录数据集名称」，与最新「分部基线 + 两层统一测试集 + 联合校准」口径不符 → git 文档恢复数据集选型/角色/两层方法论，融合流水线细节仍留 `datasets/README.md`；v2 `neutral`/`joint` 映射为第一层近似，分部基线与第二层复杂图消融登记 todo | 2026-11-02 | ✅已完成(2026-11-02) |
