# todo — 待完成记录

> 用途与使用规则见 [AGENTS.md](../AGENTS.md)「待完成事项管理」。一条记录一行、一个事项一条；完成只改状态列，**禁止删除记录**。
> 状态取值：`⬜待处理` / `🔨进行中` / `✅已完成(YYYY-MM-DD)`。
> **不设序号，按优先级从上到下排列**：后续执行按表格顺序进行；新条目按优先级插入对应位置，已完成条目移至表格末尾。

| 记录 | 登记日期 | 状态 |
|------|---------|------|
| 融合方案修订（`datasets/README.md`）：抽查只用 Qwen 3.5 4B（删 9B）、关系映射贴产品档位（关注用户/star 知识库/二跳等）、同文章多视角对照设计、`runs.jsonl` 过程记录与断点续跑 | 2026-10-23 | ✅已完成(2026-10-23) |
| 数据集落位与文档口径整理：新增 `datasets/`（不进 git，内含融合方案说明文档）承载数据集与融合细节；项目文档只记录「用融合后数据集做整体参数调整训练」+ 粗到细调参，精简全部数据集名称/细节（[social-recall.md](social-recall.md) 第 7 章等），「单独模块单独数据集训练」口径改为整体调参 | 2026-10-23 | ✅已完成(2026-10-23) |
| GanRAG v3 数据集阶段 1–2：按 [v3-dataset-design.md](v3-dataset-design.md) §10 落盘 9 份核心文件 + `environment.lock`（`datasets/configs/v3/`）；固定 T2Ranking/DISGCN/ChronoQA 版本、SHA256、许可证；完成 E01/E02a-b/E04/E09 原生数据小样验收；不启动全量物化、不改 v2 | 2026-11-08 | ✅已完成(2026-11-08) |
| GanRAG v3 融合可执行化：复用 v2 融合引擎 + `datasets/configs/v3/fusion.json` + `ds-v3-*` 命令；建 venv/numpy、下载 BM25、跑 check/smoke；不启动全量融合 | 2026-11-08 | ✅已完成(2026-11-08) |
| GanRAG v3 阶段 3–5：100–200 query 小规模 L2 + Schema/split 断言 + 共线性诊断 + 300/120 人工锚点验收；通过后冻结 interaction terms、参数范围与实验脚本 | 2026-11-08 | ⬜待处理 |
| GanRAG v3 完整 Schema 引擎：把当前 `v2-reuse` / `v3-preview` 升级为完整 v3 字段（`counterfactual_group_id`、`label_status`、`semantic_score_config_id` 等），供严格 E05/E06/E10 实验 | 2026-11-08 | ⬜待处理 |
| GanRAG v3 阶段 6–8：全量物化（2k–5k query / 20k–40k pair / ≥1,000 counterfactual group）并执行 E01–E10；最终测试集只用于最终报告 | 2026-11-08 | ⬜待处理 |
| env 配置统一到仓库根 `.env`（服务启动自动加载根 `.env`、删 `service/user/.env`、根 `.env` 补齐 JWT/Redis/邮件占位等全部变量，`.env.example` 与文档同步） | 2026-10-17 | ✅已完成(2026-10-17) |
| 引入 Redis（compose + 配置 + go-redis）+ 邮箱验证码落 Redis（`user:email_code:<scene>:<email>`，TTL 5 分钟、一次性、失败 5 次作废、发送 60s 限频）+ 验证码邮件发送通道（SMTP 落地时定） | 2026-10-15 | ✅已完成(2026-10-17) |
| users 相关代码适配（建表迁移已交付 2026-10-15）：注册/登录改「邮箱+密码」、注册接邮箱验证码、执行 `task db-gen` 并适配 store/handler 到新表结构 | 2026-10-15 | ✅已完成(2026-10-17) |
| Qwen3-Reranker-0.6B 语义精排接入与选型验证：语义召回后、时效/star/社交融合前接入；rerankScore 候选集内排名化/分位数化；禁止最终融合后全局重排；上线前在 T2Ranking 候选集对比「无重排 / Decima noul / 0.6B」（可选 bge-reranker-v2-m3），评估 NDCG@10 与 P95 延迟；见 [social-recall.md](social-recall.md) | 2026-11-02 | ⬜待处理 |
| Qwen3.5-4B 离线自动调参闭环：固定 rubric（语义相关性/社交序关系/时效合理性/star 质量）+ 校准评分头 + 3~5 次集成打分 + 温度校准；只评分不输出参数，贝叶斯优化驱动参数搜索，独立验证集复核；见 [social-recall.md](social-recall.md)「自动调参闭环」 | 2026-11-02 | ⬜待处理 |
| 第一层统一测试集与分部基线：T2Ranking + DISGCN/Dianping + ChronoQA 分部标定后合并；按用户/查询隔离切分；MVP 5~10 万 query-article 对；第二层复杂图消融见「复杂图特征消融」条目；见 [social-recall.md](social-recall.md) | 2026-11-02 | ⬜待处理 |
| Repository 服务实现（阶段二b-1：文章元信息 CRUD 4 RPC `CreateArticle`/`GetArticle`/`UpdateArticle`/`DeleteArticle`，附件行全量重建、孤儿对象清理、`article_count` 维护） | 2026-10-18 | ✅已完成(2026-11-09) |
| Repository 服务实现（阶段二b-2：正文/附件流式读取 `GetArticleContent` + `GetResource`，objectstore 流式读 + `DownloadChunk` + 相对路径防逃逸） | 2026-10-18 | ⬜待处理 |
| Repository 服务实现（阶段二b-3：`ResolveReference` 互链解析，相对路径规范化、断链统一 NotFound） | 2026-10-18 | ⬜待处理 |
| Repository 服务实现（阶段三：分片上传 5 RPC：`InitUpload`/`UploadPart`/`GetUploadStatus`/`CompleteUpload`/`AbortUpload`，断点续传与三层完整性校验） | 2026-10-18 | ⬜待处理 |
| async_tasks 重试 worker + 后台查询接口（按 `status=0` 集中处理，展示 `source_service`/`task_type`/`payload`/`last_error`） | 2026-10-18 | ⬜待处理 |
| 其他服务接入 `pkg/asynctask` 失败上报（AI / vector-store / user 按需） | 2026-10-18 | ⬜待处理 |
| AI 服务实现（建表迁移已交付 2026-10-15：`pkg/db/migrations/ganrag_ai/0001-0005`；消息信封 + 全量步骤存储对齐 UIMessage，见 [database.md](database.md)「ganrag_ai」） | 2026-10-15 | ⬜待处理 |
| 对外接口 / proto 改用 xid——**剩余范围**：`chat_sessions` / `chat_messages` 接口 xid 化与前端联调适配（users / sessions 的 proto、JWT 口径已随「users 相关代码适配」于 2026-10-17 落地，对齐 AGENTS.md「业务主键（XID）规范」） | 2026-10-15 | ⬜待处理 |
| 网关 / AI 服务流式协议对齐 Vercel AI SDK UI message stream（SSE：start / step-start / text-delta / reasoning-delta / tool-* / source / finish），支撑 useChat 原生消费（见 [web/docs/architecture.md](../web/docs/architecture.md)） | 2026-10-15 | ⬜待处理 |
| web 前端实现（P1/P2，现仅 docs；目录结构待定，见 [web/docs/architecture.md](../web/docs/architecture.md)） | 2026-10-15 | ⬜待处理 |
| 忘记密码接口（发码/验码/重置密码，重置成功后吊销该用户全部 sessions，见 [service/user/docs/architecture.md](../service/user/docs/architecture.md)；重置密码需与 E2EE 恢复码策略一起设计，保证重置后仍可查看历史消息，见 [e2ee.md](e2ee.md)） | 2026-10-15 | ⬜待处理 |
| 登录设备管理接口（设备列表 / 踢设备下线，基于 `sessions.device_id` / `device_info` / `last_active_at`） | 2026-10-15 | ⬜待处理 |
| 文档上传（pdf / docx / html → markitdown 转 md 存 Garage（`articles.content_key`），不做 OCR）（P1.5，见 [architecture.md](architecture.md)「文档转换」、[product.md](product.md)） | 2026-10-15 | ⬜待处理 |
| 多智能体路由与切换（Decima-small：当前 Agent 概率阈值低于设定值后，在全部 Agent 中重新选择）（P1.5，见 [service/ai/docs/architecture.md](../service/ai/docs/architecture.md)「多智能体架构」） | 2026-10-15 | ⬜待处理 |
| 上下文压缩（约 65% 容量触发；会话级 context + 消息游标；只保留最新隐藏 Agent 提示词并置于摘要前）（P1.5，见 [service/ai/docs/architecture.md](../service/ai/docs/architecture.md)、[data-model.md](data-model.md)「关系状态推导」） | 2026-10-15 | ⬜待处理 |
| 向量化按模型存储与检索路由（按「知识库 × 向量化模型」分集合）（P1.5，见 [service/vector-store/docs/architecture.md](../service/vector-store/docs/architecture.md)「集合与模型约定」） | 2026-10-15 | ⬜待处理 |
| P2 社交接口与界面（star / follow / 搜索 / 个人页面；表已建齐只开接口，见 [product.md](product.md)「分期路线」） | 2026-10-15 | ⬜待处理 |
| User / Repository 社交图数据导出 RPC（内部分页、xid：关注/粉丝、KB 成员/star、KB 归属；不跨库直连，供 social-graph 全量同步） | 2026-10-20 | ⬜待处理 |
| `social-graph` 服务 MVP（gograph 图构建、互关 + BFS 1~2 跳、`starred_kb`/`own_kb`/`collaborator`/`starred_author` 索引、内存快照、批量 gRPC、降级；见 [service/social-graph/docs/architecture.md](../service/social-graph/docs/architecture.md)） | 2026-10-20 | ⬜待处理 |
| AI 服务社交召回接入（候选元数据、知识库 star P95、时效乘性、社交分加性、配置化融合、Qwen3-Reranker-0.6B 语义精排、旧五档回退；见 [social-recall.md](social-recall.md)、[service/ai/docs/architecture.md](../service/ai/docs/architecture.md)「多路召回与聚合排序」） | 2026-10-20 | ⬜待处理 |
| 知识库 star P95 统计与定期刷新任务（统计参数，不是权重；供 `starScore` 归一化） | 2026-10-20 | ⬜待处理 |
| 融合数据集构建与两层统一测试集调参（分部数据集先做单模块基线与标定，再合并为第一层一阶联合校准 / 第二层复杂图特征消融；按用户/查询隔离切分；数据与融合方案细节见 `datasets/README.md`，不入 git；融合处理脚本 `datasets/scripts/` 经 `task ds-*` 分批续跑；见 [social-recall.md](social-recall.md)） | 2026-10-20 | 🔨进行中 |
| 融合数据集 v2（v2.0 唯一数据版本，取消 v2.1）：v1 核心产物归档并标注使用边界；版本化输出（`logs/runs.v2.jsonl`、`processed/v2/`、`cache/v2/`，v3 自动隔离）；修复 self_authored/public/档位优先级/relation_strict/time_fit/字段语义；BM25 + Qwen3-Embedding 向量候选 union；`neutral`/`joint` 两层 tuning 表（social/time/star treatment 一起，λ 默认 0.3/0.3/0.3）+ `tune.py` 联合扫描；QC 200+60×3 + bootstrap CI；复杂图特征仍后置；脚本/命令已交付，数据任务待执行 | 2026-10-24 | 🔨进行中 |
| 复杂图特征消融（二跳、PageRank、Leiden、Jaccard；合成社交层 + 系数扫描，确定安全区间） | 2026-10-20 | ⬜待处理 |
| Node2Vec 社交嵌入探索（时间允许时对比，不阻塞主线） | 2026-10-20 | ⬜待处理 |
| OPAQUE 认证改造（P4 聊天前置）：注册/登录/改密/重置改 RFC 9807（KE1/KE2/KE3 + `export_key`），新增 `user_e2ee` 表存 OPAQUE record + 加密 key bundle，移除 bcrypt `users.password_hash`，Gateway 多轮 auth 路由 + Redis 登录态；见 [e2ee.md](e2ee.md) | 2026-11-01 | ⬜待处理 |
| 浏览器端 E2EE 密钥栈（P4）：OPAQUE 客户端（WASM/TS 标准实现）+ WebCrypto X25519/Ed25519/AES-GCM + 不可导出密钥 + 跨设备密码恢复；先做与服务端 OPAQUE 的 RFC 9807 测试向量互通原型 | 2026-11-01 | ⬜待处理 |
| 私聊端到端加密（P4）：每条消息临时 X25519 + HKDF + AEAD + Ed25519 签名，密文存储/转发、消息序号/防重放、公钥指纹校验；Double Ratchet 后置，见 [e2ee.md](e2ee.md) | 2026-11-01 | ⬜待处理 |
| E2EE 重置密码恢复码（P4）：注册时生成一次性展示的恢复码，用 HKDF-SHA-512（高熵恢复码）+ AEAD 包装同一 key bundle；忘记密码时邮箱验证码 + 恢复码恢复，并用 Ed25519 挑战签名证明持有旧 key bundle，保证重置后仍可查看历史消息；同步「忘记密码接口」设计，见 [e2ee.md](e2ee.md) | 2026-11-01 | ⬜待处理 |
| E2EE 可用性增强（可选）：客户端加密备份（OPAQUE record + key bundle/recovery bundle 密文 + 公钥/版本）与服务端备份策略，服务器丢失数据后仍可恢复；是否纳入 P4 待定，见 [e2ee.md](e2ee.md) §2.4.4/§15 | 2026-11-01 | ⬜待处理 |
| 更新 Decima-small 多智能体路由、隐藏 Agent 系统提示词、65% 上下文压缩及向量/关键词双路召回聚合排序设计文档 | 2026-10-16 | ✅已完成(2026-10-16) |
| Repository 服务架构文档补齐（[architecture.md](architecture.md)「各服务架构详情」现为 `-`） | 2026-10-15 | ✅已完成(2026-10-17) |
| **【最高优先级·架构迁移】移除 File Store 服务，直连 Garage（S3）**：删除 `service/file-store/`、`api/proto/file-store/`、`api/gen/file-store/`；Repository 为唯一直接访问 Garage 的服务；单私有桶 + 统一对象 key `o/<object_xid>`；`attachments` 增 `relative_path`（迁移前先 `task db-status`）；前端 Markdown 相对路径 → Gateway `/api/v1/articles/{xid}/resource?path=` → Repository 权限校验 → Presigned URL 302；设计见 [service/repository/docs/architecture.md](../service/repository/docs/architecture.md)「对象存储与文件访问」 | 2026-10-17 | ✅已完成(2026-10-17) |
| 公共业务主键封装（`pkg/eid`：github.com/rs/xid 生成 `<前缀>_<xid>`（3 字母小写前缀按实体类型区分，如 `usr_`/`art_`），库存全串 `xid VARCHAR(32)`（迁移 `0004/0007_xid_prefix`），各服务统一引用、不直接用裸 xid；实体创建接线随「users 相关代码适配」「Repository/AI 服务实现」各自落地） | 2026-10-15 | ✅已完成(2026-10-17) |
| 会话模型代码适配（sessions 表迁移已交付 2026-10-15）：多端登录、(user_id, device_id) upsert、轮换原地更新 `token_hash`、改密/重置删全部会话、惰性过期清理；设备 ID 按方案 A 服务端生成/核对（取值优先级：请求体 > Gateway `did` cookie > 服务端生成 xid；Gateway 设 cookie + 登录响应回填前端 localStorage），详见 [data-model.md](data-model.md)「设备标识（device_id）口径」 | 2026-10-15 | ✅已完成(2026-10-17) |
| Repository 服务协议（proto）定义（`api/proto/repository/v1/repository.proto`，P1 内容闭环 23 RPC：知识库/目录树/文章 CRUD、互链解析、正文与资源 gRPC 流式读取（Garage 不对外暴露）、整段+会话式分片上传（断点续传 `GetUploadStatus`；三层完整性校验：单片 sha256 / 合并前结构校验 / 合并后读回 sha256 对比声明值）；`task proto` 生成，`pkg/eid` 注册上传会话前缀 `upl`） | 2026-10-18 | ✅已完成(2026-10-18) |
| 公共 async_tasks 与服务失败记录（`ganrag_common.async_tasks` + `pkg/asynctask`，业务事务内同事务 outbox，供 Repository 及后续服务 import；含 `task_type`/`payload`/幂等键/状态流转） | 2026-10-18 | ✅已完成(2026-10-18) |
| Repository 服务实现（阶段一：store/handler/gRPC server、知识库 CRUD、Garage 整段上传 `UploadWhole`、权限校验、删除走 async_tasks outbox；接口用 xid，见 `api/proto/repository/v1`、[database.md](database.md)「ganrag_repository」） | 2026-10-15 | ✅已完成(2026-10-18) |
| 整合社交关系感知召回与公开数据集调优决策到项目文档（新增 [social-recall.md](social-recall.md)、[service/social-graph/docs/architecture.md](../service/social-graph/docs/architecture.md) 与 README，更新 architecture / product / data-model / database / ai / vector-store / README / todo / fix） | 2026-10-20 | ✅已完成(2026-10-20) |
| Repository 服务实现（阶段二a：目录树 5 RPC：`CreateDirectory`/`UpdateDirectory`/`MoveDirectory`/`DeleteDirectory`/`GetDirectoryTree`，级联删除子树 + 对象清理 outbox） | 2026-10-18 | ✅已完成(2026-10-23) |
| 响应信封统一 + HTTP 对齐业务错误码：前后端通信统一 `{code, message, data}` 信封（成功 `code=200`、失败 `code=业务码`）；业务码 5 位 `HHNNN`=HTTP 码×100+序号（4 开头客户端错误、5 开头服务端错误），经 gRPC status details 跨服务携带，`pkg/errcode` 号表为唯一号源（见 [pkg/errcode/README.md](../pkg/errcode/README.md)） | 2026-10-23 | ✅已完成(2026-10-23) |
| 业务错误码常量化：`pkg/errcode/bizcode.go` 声明 `Code*` 常量（代码内唯一号源，按服务分段），哨兵声明与 Gateway 兜底码一律引用常量禁止裸数字，`TestCodeValues` 清单防撞号 | 2026-10-23 | ✅已完成(2026-10-23) |
| 时效口径收敛为单基准：时效性判断 = 文章最后修改时间 `updated_at`，无最后修改时间时 `created_at` 兜底（刚创建两值相同）；不建模 `published_at`/内容级时间。融合数据集保留单字段 `published_at` 命名（语义映射为时效基准时间），不涉及召回时间权重数值；`timeDecay` 标定加「弱影响」约束（影响显著小于语义/社交，半衰期以长档为主）；同步 data-model / database / social-recall / datasets/README | 2026-10-24 | ✅已完成(2026-10-24) |
| 端到端加密方案文档落地：新增 [e2ee.md](e2ee.md)（OPAQUE RFC 9807 + key bundle + 恢复码 + 私聊消息层 + 威胁模型 A/服务端重置密码与离线猜密码残余风险），同步 README / architecture / data-model / database / product / user 架构 / fix / todo | 2026-11-01 | ✅已完成(2026-11-01) |
| 文档对齐最新召回方案：重排模型改 Qwen3-Reranker-0.6B（语义召回后、最终融合前；`w_audit` 退出主公式，Decima `noul` 降为语义路可选排序器），新增 Qwen3.5-4B 离线调参评分闭环、两层统一测试集与公开数据集选型口径；见 [social-recall.md](social-recall.md) | 2026-11-02 | ✅已完成(2026-11-02) |
