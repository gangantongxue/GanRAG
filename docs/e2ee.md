# 端到端加密（E2EE）与 OPAQUE 认证方案

> **状态**：设计定稿，代码未实现（2026-11-01）。
> **威胁模型**：A —— 服务器可能被入侵/好奇，但不主动修改客户端代码；不防恶意前端（见 §2.1 / §2.4）。
> **适用阶段**：P4 用户间私聊（聊天功能前置的登录/用户表改造）；AI 聊天/RAG **不适用**。
> **执行跟踪**：[todo.md](todo.md)「OPAQUE 认证改造」「浏览器端 E2EE 密钥栈」「私聊端到端加密」「E2EE 重置密码恢复码」；设计冲突登记见 [fix.md](fix.md) `FIX-027`。
> **相关文档**：[architecture.md](architecture.md)、[data-model.md](data-model.md)、[database.md](database.md)、[service/user/docs/architecture.md](../service/user/docs/architecture.md)。

## 1. 定位与范围

本方案采用基于 **OPAQUE（RFC 9807）** 的 **aPAKE（Asymmetric Password-Authenticated Key Exchange，非对称口令认证密钥交换）** 思路，实现：

- 用户仅凭密码即可在换设备、换浏览器、清空浏览器数据后恢复端到端加密所需的长期私钥；
- 服务器参与认证和密钥材料托管，但**在协议中不获知明文密码**，**不能解密用户长期私钥**，**不能解密私聊消息**；
- 不依赖浏览器本地长期保存密钥作为唯一恢复手段；
- 用户间私聊消息端到端加密，服务器只转发/存储密文和公开材料；
- 修改密码只重新包装 key bundle（OPAQUE 注册记录随新密码重新生成），长期身份密钥不变，历史消息仍可解密；
- 忘记密码时，凭**恢复码**恢复同一把长期私钥，重置密码后仍可查看历史消息。

### 1.1 适用与不适用

| 场景 | 是否 E2EE | 原因 |
|---|---|---|
| 用户间私聊（未来 P4） | ✅ 本方案 | 服务器只需转发密文，不需要读取内容 |
| AI 聊天 / RAG 问答 | ❌ | 服务端必须读取查询与知识库内容做检索、向量化和生成，无法端到端加密 |
| 知识库文章 / 附件 | ❌ | 服务端需要全文索引、向量化、Markdown 渲染与权限判定 |
| 登录密码 / 邮箱验证码 | 密码走 OPAQUE + TLS；验证码走服务端校验 | 密码不离开客户端；验证码仍由服务端校验邮箱归属 |

> **重要原则**：不得自行设计或拼接密码学原语。必须使用经过审计的 OPAQUE/aPAKE 标准实现和标准 ciphersuite；客户端与服务端必须使用同一套件，并用 RFC 9807 测试向量验证互通。

### 1.2 非目标（YAGNI）

- 不隐藏元数据（登录时间、消息大小/频率、通信关系等服务器仍可见）；
- 不防御已被攻陷的客户端、恶意浏览器扩展、屏幕录制、键盘记录；
- 不做匿名通信、阅后即焚、群聊、多设备独立密钥（后续按需求扩展）；
- 不承诺抗量子（后续可换 PQC ciphersuite，协议层保持版本化）。

## 2. 威胁模型与安全属性

### 2.1 假设

- 传输层使用 TLS 1.3+；OPAQUE 注册要求服务端认证的机密通道（RFC 9807 §3.2）。
- **威胁模型 A（本项目默认）**：服务器可能「诚实但好奇」、被入侵或拖库，但**不主动修改客户端代码**；如果服务端会下发恶意前端，E2EE 无法防御（见 §2.4.3）。
- 客户端代码可信；XSS、恶意扩展、供应链攻击不在 E2EE 防护范围内。
- 用户密码强度由密码策略与 Argon2id KSF 共同保障；弱密码仍是最大风险。

### 2.2 能提供的保证

> 以下保证以 §2.1 的威胁模型 A、客户端可信、用户使用强密码为前提；例外与残余风险见 §2.3 / §2.4。

| 属性 | 说明 |
|---|---|
| 服务器不可见密码 | OPAQUE 的 OPRF 盲化使服务器在注册和登录中均看不到密码与 OPRF 输出 |
| 抗预计算攻击 | OPRF 密钥由服务端秘密派生；没有服务端 `oprf_seed` 无法预计算彩虹表 |
| 服务器不可解密长期私钥 | key bundle 由 `export_key` 派生的 KEK 加密，服务器不持有 `export_key` |
| 服务器不可解密私聊 | 会话密钥由双方私钥协商；服务器只有公钥 |
| 换设备/清缓存可恢复 | 凭密码重新执行 OPAQUE 登录，解密 key bundle |
| 改密不丢历史 | 长期身份密钥不变，仅重新包装 key bundle |
| 重置密码可看历史 | 凭注册时生成的恢复码解密同一 key bundle（恢复码丢失则无法恢复） |
| 邮箱被攻陷不泄露历史消息 | 无恢复码时，攻击者只能接管账号/重置密码，不能解密旧 key bundle；历史私聊仍受保护 |

### 2.3 不能提供的保证（必须如实告知）

- **服务器 + 数据库 + `oprf_seed` 同时泄露时，弱密码可被离线字典攻击**。OPAQUE 防的是「预计算/彩虹表」，不是「服务器被攻陷后的离线猜测」；Argon2id + 强密码策略只能提高成本。
- **`oprf_seed` 是全局限定符**：一旦泄露，攻击者可对所有用户做预计算/离线猜测；必须按最高级别保护（密钥管理、最小权限、备份加密），轮换意味着全员重新注册。
- **服务器可主动作恶**：恶意服务器可以替换用户公钥、拒绝服务或提供伪造的 key bundle。客户端需展示公钥指纹/安全码并做 TOFU 校验；本方案不解决「首次接触即被 MITM」的信任问题。
- **恢复码丢失 = 历史私聊永久不可解密**。邮箱验证码只能证明账号归属，不能恢复 E2EE 私钥；产品界面必须在重置前明确提示。
- **元数据可见**：服务器仍知道谁在何时给谁发了多大、多少条消息。
- **客户端被攻陷后无解**：XSS/恶意扩展可读取内存中的明文与私钥。

### 2.4 服务端主动攻击与密码离线猜测（威胁模型 A）

> 本节回答一个关键问题：服务器已经拿到数据库、甚至能重置账号密码，它能不能读旧聊天记录？结论是**仅重置密码不能**，但**离线猜弱密码可以**；后者必须作为残余风险明确记录，不能忽略。

#### 2.4.1 重置账号密码 ≠ 重置 E2EE 密钥

1. 服务端用邮箱验证码重置账号密码，替换该用户的 OPAQUE 记录；客户端下次登录得到的是**新的 `export_key`**。
2. 旧的 `key_bundle` 仍然由**旧 `export_key`** 加密；新密码派生的 `export_key` 与旧的不同，解不开旧密文。
3. 服务端也没有旧密码或恢复码，无法重新加密/解密旧 `key_bundle`；`recovery_bundle` 由 128 位恢复码保护，同样解不开。`recovery_bundle` **不是密码重置令牌**，不能帮助服务端推导旧密码或恢复码。
4. 服务端最多能把用户锁在门外，或生成一套新 key bundle（并替换公钥）去读**新**消息（客户端若做了指纹校验可发现）；**旧聊天记录仍然不可解密**。
5. 想读旧记录，只能：离线猜中旧密码、拿到恢复码、或攻陷客户端（见 §2.4.2–2.4.3）。

#### 2.4.2 离线猜密码是固有限制

- 服务端持有 `oprf_seed`、`opaque_record`、`key_bundle` 密文，可以对密码候选离线计算 OPRF/`export_key` 并尝试解密 `key_bundle`。
- OPAQUE 防的是「预计算/彩虹表」，**不防服务端拖库后的离线字典攻击**；Argon2id 提高单次猜测成本，强密码 + 密码泄露检测降低成功率，但不能把弱密码变成不可猜。
- 若要求「即使服务端拿到 DB 也绝对不能解密」，就不能只用密码作为 key bundle 的保护密钥；需要引入高熵主密钥、Passkey/WebAuthn PRF 或设备密钥（见 §15）。代价是**不能做到纯密码恢复**。
- 恢复码是 128 位随机值，离线猜恢复码不可行；若未来允许用户自选恢复短语，必须用 Argon2id + 熵检查，否则会成为比密码更弱的入口。

#### 2.4.3 公钥替换与恶意前端

- 服务端可以替换用户的 X25519/Ed25519 公钥，诱导他人把新消息加密给攻击者；客户端必须展示指纹/安全码并做 TOFU 校验，但**首次接触**无法完全防御。
- 服务端可以下发恶意前端（JS/WASM）直接窃取密码、`export_key`、私钥或恢复码。这超出 E2EE 防护范围；威胁模型 A 明确不防这一层。若要求防主动恶意服务端，必须使用可信客户端分发（原生 App、签名桌面端、独立静态托管 + 完整性校验）。
- 服务端可以删除/替换密文：AEAD 与公钥校验能发现篡改，但删除/拒绝提供是可用性问题（见 §2.4.4）。

#### 2.4.4 可用性不是 E2EE 的保证

- 服务端丢失 `opaque_record`、`key_bundle` 或 `recovery_bundle`，用户将无法登录或恢复；E2EE 只保证机密性，不保证可用性。
- 局域网自部署应做服务端备份/复制；可选增加**客户端加密备份**（导出 OPAQUE record + key bundle/recovery bundle 密文 + 公钥/版本），服务器丢失后可导入恢复。是否纳入 P4 见 §15 待决策。

#### 2.4.5 攻击路径速查

| 攻击路径 | 能否读旧聊天 | 说明 |
|---|---|---|
| 重置账号密码（邮箱验证码） | ❌ | 新 `export_key` 解不开旧 key bundle；只能锁定用户 |
| 重置并生成新 key bundle | ❌ | 新密钥只能读新消息；旧历史仍用旧密钥 |
| 直接解密 `recovery_bundle` | ❌ | 需要 128 位恢复码，不在服务端 |
| 离线猜旧密码 | ⚠️ 弱密码可以 | 服务端有 `oprf_seed` + `opaque_record` + `key_bundle`；Argon2id 提高成本 |
| 猜恢复码 | ❌ | 128 位随机值不可行；用户自选弱短语则例外 |
| 替换用户公钥 | ⚠️ 可读新消息 | 需客户端指纹/TOFU；恶意前端可绕过 |
| 下发恶意前端 | ⚠️ 可读全部 | 超出 E2EE 防护范围；需可信客户端 |
| 删除/篡改密文 | ❌/DoS | AEAD + 公钥校验防篡改；删除是可用性问题 |

## 3. 术语

| 术语 | 含义 |
|---|---|
| **OPAQUE** | RFC 9807 定义的 aPAKE，组合 OPRF、密钥恢复机制与 AKE |
| **aPAKE** | 非对称口令认证密钥交换；服务器不存储等价明文密码，协议中不获知密码 |
| **OPRF** | Oblivious Pseudorandom Function，不经意伪随机函数；服务器参与计算但看不到客户端输入和最终输出 |
| **盲化 / 去盲** | 客户端用随机盲化因子变换密码输入；服务器处理后，客户端去盲得到 OPRF 输出 |
| **RegistrationRecord** | OPAQUE 服务端保存的注册记录：`client_public_key` + `masking_key` + `Envelope` |
| **Envelope** | OPAQUE 自身的 `{envelope_nonce, auth_tag}`，用于校验密钥恢复，**不是**应用私钥容器 |
| **`export_key`** | OPAQUE 客户端在注册/登录完成时得到的、仅客户端持有的密钥根；用于派生应用层 KEK |
| **key bundle** | 应用层私钥包：Ed25519 私钥种子 + X25519 私钥，经 KEK 加密后存服务器 |
| **KDF / HKDF** | 密钥派生函数；本项目用 HKDF-SHA-512 做用途隔离 |
| **KSF** | Key Stretching Function，抗离线字典攻击的慢哈希；本项目用 Argon2id |
| **AEAD** | 带关联数据的认证加密，如 AES-256-GCM、ChaCha20-Poly1305 |
| **AKE** | 认证密钥交换；OPAQUE 登录阶段用 3DH 完成隐式认证并建立会话密钥 |
| **KE1/KE2/KE3** | OPAQUE 登录阶段的三条消息 |
| **长期身份密钥** | 用户的 Ed25519 签名密钥 + X25519 协商密钥；私钥受 key bundle 保护，公钥可公开 |
| **恢复码** | 注册时生成的高熵随机码；可重复使用，轮换后失效；用于在忘记密码时解密同一 key bundle |

## 4. 总体架构

```
┌─────────────────────────────────────────────────────────────┐
│ 客户端（Web，未来实现）                                      │
│  · 密码只在本机内存                                          │
│  · OPAQUE 客户端（WASM/TS 标准实现）                         │
│  · WebCrypto / 审计过的曲线库：X25519、Ed25519、AES-GCM      │
│  · key bundle 明文只在本机；本地缓存不是唯一恢复手段         │
└───────────────┬─────────────────────────────────────────────┘
                │ TLS；只传 OPAQUE 消息、密文、公钥
┌───────────────▼─────────────────────────────────────────────┐
│ Gateway                                                      │
│  · 多轮 auth 路由（register/login/reset start/finish）       │
│  · 限流、审计、{code,message,data} 信封                      │
└───────────────┬─────────────────────────────────────────────┘
                │ gRPC
┌───────────────▼─────────────────────────────────────────────┐
│ User 服务                                                    │
│  · OPAQUE 服务端（RFC 9807）                                 │
│  · 保存 RegistrationRecord、key bundle 密文、公钥、套件版本   │
│  · 不保存密码、export_key、私钥明文                          │
│  · 登录成功后签发现有 access/refresh token                    │
└───────┬───────────────────────────────┬─────────────────────┘
        │                               │
┌───────▼────────┐              ┌───────▼──────────────┐
│ MySQL          │              │ Redis                │
│ user_e2ee      │              │ OPAQUE 登录短态       │
│ sessions       │              │ user:opaque_login:*  │
└────────────────┘              └──────────────────────┘

未来聊天服务（P4 立项时定，未建）：
  · 只存/转发消息密文与公开头（会话 ID、序号、时间、临时公钥、nonce）
  · 不持有任何用户私钥，不能解密
```

### 4.1 三层职责

1. **认证与密钥恢复层**：OPAQUE RFC 9807。服务器用全局 `oprf_seed` + `credential_identifier` 派生每用户 OPRF 密钥；登录用 3DH AKE 完成隐式认证。
2. **长期身份层**：Ed25519 签名密钥 + X25519 协商密钥，两把独立密钥；私钥打包为 key bundle，用 `export_key` 派生的 KEK 加密后托管。
3. **消息加密层**：X25519 + HKDF-SHA-512 + AEAD + Ed25519 签名；每条消息使用临时 X25519 密钥（P4 MVP），完整前向保密由 Double Ratchet 后续提供。

## 5. 密钥与数据分工

### 5.1 客户端持有

- 用户明文密码：仅存在于客户端内存和用户输入中，不上传服务器。
- OPAQUE `export_key`：注册/登录完成后由客户端计算，不发送服务器。
- key bundle 明文：Ed25519 私钥种子、X25519 私钥；仅存在于客户端。
- 恢复码：注册时生成，仅用户保存；不上传服务器。
- 本地缓存（可选）：IndexedDB 等，仅用于提升体验，不得作为唯一恢复途径。

### 5.2 服务器保存

| 数据 | 位置 | 说明 |
|---|---|---|
| `oprf_seed` | 配置/密钥管理 | OPAQUE 全局 OPRF 种子；长期稳定；不入库 |
| `server_private_key` / `server_public_key` | 配置/密钥管理 | OPAQUE AKE 服务端密钥对；长期稳定；不入库 |
| `RegistrationRecord` | `user_e2ee.opaque_record` | `client_public_key`、`masking_key`、`Envelope` 的序列化 |
| key bundle 密文 | `user_e2ee.key_bundle` | `export_key` 派生 KEK 加密；服务器无法解密 |
| recovery bundle 密文 | `user_e2ee.recovery_bundle` | 恢复码派生 KEK 加密；服务器无法解密 |
| Ed25519 / X25519 公钥 | `user_e2ee.*_public_key` | 公开材料；供其他用户获取 |
| 套件/版本 | `user_e2ee` | `opaque_suite`、`key_bundle_version`、`recovery_version` |
| token / 会话 | `sessions` | 与现役一致；OPAQUE 认证通过后签发 |

### 5.3 服务器绝不保存

- 用户明文密码；
- OPRF 最终输出 / `randomized_password` / `export_key`；
- 用于解密 key bundle 或 recovery bundle 的 KEK；
- 用户长期私钥明文；
- 私聊消息明文。

> `masking_key` 是 OPAQUE 注册记录的一部分，用于防客户端枚举，**不是密码等价物**；但它仍是敏感材料，按密钥材料保护。

## 6. 注册流程

前置：邮箱验证码校验通过；TLS 1.3+；客户端与服务端已协商同一 OPAQUE ciphersuite。

1. 用户设置密码（客户端做长度/强度校验）。
2. 客户端执行 OPAQUE `CreateRegistrationRequest(password)`，生成盲化输入与 `blind`。
3. 客户端把 `RegistrationRequest` 发给 User 服务。
4. 服务端在验证码校验后创建（或预创建）账号，用全局 `oprf_seed` + `credential_identifier = user_xid` 派生 OPRF 密钥，执行 `CreateRegistrationResponse`，返回 `evaluated_message`、`server_public_key` 与 `client_identity = user_xid`。
5. 客户端执行 `FinalizeRegistrationRequest`，去盲、拉伸、得到 `export_key` 与 `RegistrationRecord`（`client_public_key` / `masking_key` / `Envelope`）。
6. 客户端生成长期身份密钥：
   - Ed25519 签名密钥对；
   - X25519 协商密钥对。
7. 客户端把两把私钥按版本化格式打包为 key bundle：
   ```
   KeyBundleV1 {
     version: u16
     ed25519_private_seed: 32 bytes
     x25519_private_key:   32 bytes
   }
   ```
8. 客户端派生 KEK 并加密 key bundle：
   - `kek = HKDF-SHA-512(export_key, salt = SHA-512("GanRAG-E2EE-KeyBundle" || user_xid), info = "GanRAG-E2EE-KeyBundle-v1" || opaque_suite)`
   - `key_bundle = AES-256-GCM(kek, nonce, plaintext = KeyBundleV1, aad = user_xid || key_bundle_version)`
   - `nonce` 为 12 字节随机值。
9. 客户端生成恢复码并加密同一 key bundle：
   - 恢复码：128 位随机值，Crockford Base32 编码 26 字符，分组展示（如 `XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XX`）；
   - `recovery_kek = HKDF-SHA-512(recovery_code, salt = recovery_salt, info = "GanRAG-E2EE-Recovery-v1")`；
   - `recovery_bundle = AES-256-GCM(recovery_kek, recovery_nonce, plaintext = KeyBundleV1, aad = user_xid || recovery_version)`；
   - 恢复码只在客户端展示一次，不上传；`recovery_salt`（16 字节随机）随密文上传。
10. 客户端上传：`RegistrationRecord`、`key_bundle` 密文、`key_bundle_nonce`、`recovery_bundle` 密文、`recovery_nonce`、`recovery_salt`、两把公钥、`opaque_suite`、版本号。
11. 服务端校验长度/编码/公钥合法性，写入 `user_e2ee`（每用户一行，事务内完成）；不解析 key bundle 内容。
12. 客户端强制用户确认已保存恢复码：提供下载/打印/密码管理器保存指引，**不得只存浏览器 localStorage/IndexedDB**（清缓存即丢失）；随后丢弃 `export_key`、私钥明文、恢复码明文（内存清理）。

> 若产品允许用户跳过恢复码，`recovery_*` 列可改为 NULL，但必须在 UI 明确「重置密码将丢失历史私聊」；默认建议强制生成。

## 7. 登录流程

1. 用户输入邮箱 + 密码；客户端执行 OPAQUE `GenerateKE1(password)`，生成 `KE1`（含盲化元素、临时公钥、nonce）。
2. 客户端把 `KE1` + 邮箱发给 User 服务（经 Gateway）。
3. 服务端按邮箱查 `users`，再取对应 `user_e2ee`：
   - 存在：用 `opaque_record`、`oprf_seed`、`server_private_key` 执行 `GenerateKE2`；
   - 不存在：按 RFC 9807 §10.9 生成**伪记录**，返回结构合法的 `KE2`，避免账号枚举；只有后续 `KE3` 失败。
4. 服务端把 `KE2`、一次性 `login_id`、`client_identity = user_xid` 与固定 `server_identity` 返回客户端；客户端用它们执行 `GenerateKE3`。服务端 AKE 临时状态写 Redis `user:opaque_login:<login_id>`，TTL 约 2 分钟。
5. 客户端执行 `GenerateKE3`：
   - 从 `KE2` 的 `CredentialResponse` 取出 `Envelope`，用密码恢复 OPAQUE 客户端密钥；
   - 得到 `export_key` 与会话密钥；
   - 生成 `KE3`。
   - 密码错误时 `Envelope` 校验失败，客户端**不发送 KE3**，本地报「邮箱或密码错误」。
6. 客户端发送 `KE3` + `login_id`。
7. 服务端执行 `ServerFinish(KE3)` 验证：
   - 失败：返回统一 `Unauthenticated`，删除 Redis 临时状态；
   - 成功：删除临时状态，签发 access/refresh token，写入/更新 `sessions`（与现役一致）。
8. 服务端在登录成功响应中返回 `key_bundle` 密文、`key_bundle_nonce`、`key_bundle_version`、公钥、套件标识。
9. 客户端用 `export_key` 派生 KEK，解密 key bundle，校验得到的 Ed25519/X25519 公钥与服务器返回的公钥一致；不一致则拒绝使用并告警（防服务器替换）。
10. 客户端进入已登录状态；私钥只留在内存/受保护的本地存储。

> 服务端不是通过保存「密码哈希」比对密码，而是通过 OPAQUE AKE 的 `KE3` 验证客户端是否恢复了正确密钥材料。服务器全程看不到密码和 `export_key`。

### 7.1 登录态与限流

- `login_id` 一次性，Redis TTL 约 2 分钟；`ServerFinish` 成功/失败都删除。
- 登录 start/finish 走 Gateway 的 auth 限流桶；同一邮箱/IP 失败计数与退避，防在线爆破。
- 不因「邮箱不存在」返回不同错误；注册/登录均防枚举。
- OPAQUE 消息体积小，但需限制 base64 长度、元素合法性，防 DoS/畸形输入。

## 8. 修改密码

1. 用户输入旧密码，按登录流程完成 OPAQUE `KE1/KE2/KE3`，恢复 key bundle 明文。
2. 用户输入新密码；客户端按注册流程用新密码执行 OPAQUE 注册两轮，得到新的 `export_key` 与新 `RegistrationRecord`。
3. 客户端用新 `export_key` 重新加密**同一把** key bundle，得到新 `key_bundle` 密文。
4. 客户端本地先解密验证新密文可还原同一公钥，确认新密码可用。
5. 客户端上传新 `RegistrationRecord` + 新 `key_bundle` 密文 + 新 nonce/版本。
6. 服务端事务内替换 `user_e2ee` 的 OPAQUE record 与 key bundle 字段；恢复码包装保持不变（用户已保存的恢复码继续有效）。
7. 吊销该用户全部 `sessions`（沿用现役「改密后重新登录」语义）。

原则：改密只更换 key bundle 的保护密钥，不重新加密历史消息；长期身份密钥不变。

## 9. 忘记密码与恢复（恢复码方案）

### 9.1 目标

用户忘记密码时，凭**邮箱验证码 + 恢复码**恢复同一把长期私钥，并用恢复出的 Ed25519 私钥对服务端一次性挑战签名、证明持有旧 key bundle；重置密码后仍可查看历史私聊。

### 9.2 流程

1. 用户选择「忘记密码」，输入邮箱，获取并校验邮箱验证码（scene=`reset_password`）。
2. 服务端校验验证码后，返回该用户的 `recovery_bundle` 密文、`recovery_nonce`、`recovery_salt`、`recovery_version`、公钥、套件标识。
3. 客户端要求用户输入恢复码；用 `recovery_kek = HKDF-SHA-512(recovery_code, salt=recovery_salt, info="GanRAG-E2EE-Recovery-v1")` 解密 `recovery_bundle`，得到 key bundle。
4. 客户端校验解出的 Ed25519/X25519 公钥与服务器返回的公钥一致；不一致则拒绝（防替换/损坏）。
5. 用户设置新密码；客户端按注册流程用新密码执行 OPAQUE 注册，得到新 `RegistrationRecord` 与新 `export_key`；用新 `export_key` 重新加密同一 key bundle，计算新 record / 新 key bundle 摘要。
6. 恢复授权：服务端下发一次性挑战（Redis `user:e2ee_recovery:<user_xid>`，TTL 5 分钟）；客户端用恢复出的 Ed25519 私钥对 `用途标签 || user_xid || challenge || 新 record 摘要 || 新 key bundle 摘要` 签名；上传新 record + 新 key bundle + 挑战签名。
7. 服务端用 `ed25519_public_key` 验证挑战签名与摘要；验证通过才替换 `user_e2ee`，否则拒绝（不得留下半更新状态）；随后吊销全部 sessions，用户用新密码重新登录。
8. 历史消息仍可用同一长期私钥解密。

### 9.3 恢复码丢失

- 邮箱验证码只能证明账号归属，**不能解密 recovery_bundle**。
- 若用户没有恢复码：只能走「放弃历史消息」重置——生成新 key bundle（新身份密钥）与新的恢复码，替换 record 与 `recovery_*`；旧私聊永久不可解密。该路径无法完成第 6 步挑战签名，服务端应记录审计，UI 必须明确警告并提供「我确认放弃历史消息」二次确认。
- 恢复码不应上传服务器、不应写入日志；服务端只存其加密后的 bundle。
- 挑战签名只证明客户端持有旧 key bundle，不替代登录 AKE；挑战一次性、短 TTL、绑定 user_xid 与新 record/key bundle 摘要，防重放。

### 9.4 恢复码轮换

- 用户可在设置页（已登录、已验证密码）重新生成恢复码：客户端用新恢复码重新包装当前 key bundle，替换 `recovery_*` 字段；旧恢复码立即失效。
- 恢复码轮换不影响 `key_bundle` 与历史消息。

## 10. 私聊消息加密

### 10.1 身份与公钥分发

- 每个用户有 Ed25519 签名公钥与 X25519 协商公钥，存 `user_e2ee`，由 User 服务提供给需要发起会话的用户。
- 客户端首次获取对端公钥时展示指纹/安全码；用户可线下/带外比对（TOFU）。服务器替换公钥可被指纹比对发现，但首次接触不解决信任问题。
- 公钥需校验编码/曲线点合法性；拒绝低阶点/无效点。

### 10.2 P4 MVP：每条消息临时 X25519 + Ed25519 签名

发送方：

1. 生成临时 X25519 密钥对 `(esk, epk)`。
2. 计算 `shared = X25519(esk, recipient_x25519_public_key)`。
3. 派生消息密钥：
   - `salt = conversation_id`（或会话 ID 的 SHA-512）；
   - `info = "GanRAG-Chat-v1" || sender_xid || recipient_xid || msg_id`；
   - `key = HKDF-SHA-512(shared, salt, info, 32 bytes)`。
4. 生成 12 字节随机 `nonce`；构造消息头：
   ```
   ChatHeaderV1 {
     version: u16
     conversation_id: bytes
     msg_id / seq: u64
     sender_xid: string
     recipient_xid: string
     timestamp: u64
     ephemeral_public_key: 32 bytes
     nonce: 12 bytes
     suite: string
   }
   ```
5. `ciphertext = AES-256-GCM(key, nonce, plaintext, aad = canonical(ChatHeaderV1))`。
6. `signature = Ed25519_sign(sender_ed25519_private_key, canonical(ChatHeaderV1) || ciphertext)`。
7. 发送 `ChatHeaderV1 || ciphertext || signature` 给服务器；立即清除 `esk` 与 `key`。
8. 服务器只存/转发密文包；可按 `conversation_id`、`seq`、`timestamp` 排序，但看不到内容。

接收方：

1. 用 `sender_xid` 取发送方 Ed25519 公钥，验证 `signature`；失败则丢弃并告警。
2. 计算 `shared = X25519(recipient_x25519_private_key, ephemeral_public_key)`。
3. 按相同 HKDF 参数派生 `key`，用 `nonce` 与 AAD 解密；失败则丢弃。
4. 校验 `seq`/`msg_id` 防重放；按会话维护接收窗口。

### 10.3 安全属性与上限（必须如实标注）

- **发送方长期 X25519 私钥泄露**：不能解密其已发送消息（消息加密未用发送方长期私钥）。
- **接收方长期 X25519 私钥泄露**：可解密发给该用户的历史消息（攻击者可用 `epk` + 接收方静态私钥重算共享秘密）。因此 **P4 MVP 不提供完整前向保密**。
- **无 post-compromise security**：长期密钥泄露后，后续消息仍可被解密/伪造（签名密钥泄露可伪造）。
- 产品文案不得把 P4 MVP 宣传为「完整前向保密」；完整前向保密与 PCS 由 P4-3 Double Ratchet 提供。

### 10.4 P4-3：Double Ratchet（后续）

- 引入 X3DH 预密钥（签名预密钥、一次性预密钥）与 Double Ratchet（DH ratchet + 对称 ratchet）。
- 提供完整前向保密与 post-compromise security；支持乱序/跳过的消息密钥。
- 必须使用经过审计的实现（如 Signal 协议库），不得自研 ratchet。
- 预密钥发布、一次性预密钥耗尽、设备/会话状态管理需单独设计。

### 10.5 防重放与顺序

- 每会话单调 `seq`；接收端维护窗口，拒绝重复/过旧消息。
- 服务器可校验 `seq` 连续性/去重，但不能替代接收端校验。
- 消息删除/保留策略在聊天服务立项时定；E2EE 不阻止服务器删消息（可用性风险仍在）。

### 10.6 多设备

- P4 MVP：所有设备共享同一 key bundle（密码/恢复码恢复），同一私钥可解密所有设备收到的消息。
- 风险：任一设备被攻陷即泄露长期私钥；无法按设备撤销。
- 后续可选：每设备独立密钥 + 设备授权/密钥转发；需单独设计，不在本期。

## 11. 算法套件与搭配约束

盲化、OPRF、KDF、AEAD、哈希、曲线不能随意搭配，必须绑定在标准 ciphersuite 中；客户端与服务端必须使用同一套件。

### 11.1 OPAQUE 套件（建议）

| 组件 | 选择 |
|---|---|
| OPRF | ristretto255-SHA512（RFC 9497） |
| KDF | HKDF-SHA-512 |
| MAC | HMAC-SHA-512 |
| Hash | SHA-512 |
| AKE Group | ristretto255（3DH） |
| KSF | Argon2id（m=64 MiB, t=3, p=1） |

也可以选 P-256 系列套件（OPRF P256-SHA256 + SHA-256 组件），但必须整套切换并重新注册；不得混用。

### 11.2 应用层套件

| 用途 | 选择 |
|---|---|
| 长期签名 | Ed25519（RFC 8032） |
| 长期/临时协商 | X25519（RFC 7748） |
| KDF | HKDF-SHA-512（RFC 5869） |
| AEAD | AES-256-GCM（WebCrypto 原生；备选 ChaCha20-Poly1305） |
| 恢复码编码 | Crockford Base32，128 位随机值 |

### 11.3 版本化与互通

- `opaque_suite`、`key_bundle_version`、`recovery_version`、`ChatHeaderV1.version` 必须持久化/传输；未知版本拒绝，不猜测。
- OPAQUE 身份绑定：`client_identity = user_xid`，`server_identity = 部署域名/固定服务标识`；两者参与 Envelope/AKE transcript，注册与登录必须一致；身份变更需重新注册。
- OPRF 种子口径：采用全局 `oprf_seed` + `credential_identifier` 派生每用户 OPRF key（RFC 9807 推荐）；不采用「每用户独立 seed」变体，避免偏离标准实现与枚举风险。
- 服务端与客户端必须通过 RFC 9807 官方测试向量 + 跨实现互通测试（例如 Go 服务端与浏览器 WASM 客户端）。
- 候选实现（需原型验证与安全评审，不直接锁版本）：
  - 服务端：`github.com/bytemare/opaque`（Go，RFC 9807，RFC 作者维护，未独立审计）；
  - 客户端：`opaque-ke`（Rust 核心库经 NCC Group 审计）的 WASM 封装，或等价的 RFC 9807 实现；封装层本身仍需评审；
  - 不使用仅实现 draft-07 的库（如 `@cloudflare/opaque-ts`）作为最终实现。
- 浏览器 WebCrypto 对 X25519/Ed25519 支持不完全一致；不可用时使用审计过的 JS/WASM 曲线库，禁止自研。

## 12. 工程实现注意事项

### 12.1 客户端

- 密码、`export_key`、key bundle 明文、恢复码只在内存；用后清零（尽力而为）。
- 优先使用 WebCrypto 的不可导出密钥对象；IndexedDB 缓存不是唯一恢复手段。
- OPAQUE 客户端建议 WASM（性能与常量时间更可控）；CSP 需允许 `wasm-unsafe-eval`，同时严格限制脚本源。
- 防 XSS、恶意扩展、供应链攻击；依赖锁版本 + 审计；E2EE 不能防御已被攻陷的客户端。
- 恢复码展示一次，强制用户确认保存；不得写入日志/埋点。

### 12.2 服务端

- `oprf_seed`、`server_private_key` 放密钥管理/配置，不入库；备份与访问审计；轮换意味着所有用户重新注册（Envelope 绑定 `server_public_key`），非必要不轮换。
- `user_e2ee` 的密文字段按敏感数据保护：最小权限、备份加密、禁止明文日志。
- OPAQUE 消息做长度/编码/曲线点校验；登录/注册接口限流；防枚举伪记录。
- 密码策略：最低长度 + 常见弱密码/泄露检测；Argon2id 参数按威胁模型 A 评审（提高离线猜测成本，不承诺消除）。
- Redis 登录短态与恢复挑战均一次性、TTL 短；key 规范 `user:opaque_login:<login_id>`、`user:e2ee_recovery:<user_xid>`。
- `sessions`/双 token 模型不变；改密/重置吊销全部会话。
- 聊天服务（未来）只存/转发密文与公开头；不得记录消息明文、私钥、`export_key`、恢复码。

### 12.3 兼容与迁移

- 现役 `users.password_hash`（bcrypt）与 OPAQUE 目标态冲突，需迁移：
  - **一次性切换**：确认无生产用户/可清库 → `0006_user_e2ee`（建表）→ 切换完成后 `0007_drop_password_hash`（删列）；
  - **双栈过渡**：保留可空 `password_hash`，老用户首次登录后强制 OPAQUE 注册，全部迁移后再删列。
- 修改迁移前必须 `task db-status`；已应用迁移禁改，新增 up/down 成对。
- 旧客户端/旧接口在过渡期保留 `POST /auth/login`（bcrypt），新客户端走 OPAQUE 多轮路由；下线时间由产品定。
- `user_e2ee` 每用户一行；改密/重置原地替换；不做密钥历史表（YAGNI，后续需要再迁移）。

## 13. 常见误区与禁止事项

1. **「私钥加密、公钥解密」不是保密加密**。公钥公开，任何人都能验证；准确说法是「私钥签名、公钥验证」，用于身份认证和完整性。
2. **公钥加密的内容不能再用公钥解密**。公钥加密后只有对应私钥能解密。
3. **签名不是加密**。签名对消息哈希和上下文运算，验证方用公钥验证签名是否有效，不是「解出原文」。
4. **阶段性值不等于最终对称密钥**。OPRF 输出 → `randomized_password` → `export_key` → KEK，每一步都要用标准 KDF 做用途隔离，不能把 OPRF 输出直接当 AES 密钥。
5. **本地缓存不能作为唯一密钥恢复手段**。浏览器 IndexedDB 不是绝对安全保险箱；密码 + 恢复码才是恢复路径。
6. **OPAQUE 的 Envelope 不是应用层 key bundle**。RFC 9807 的 Envelope 只含 `envelope_nonce` + `auth_tag`；应用私钥包是另一层，用 `export_key` 加密。
7. **OPAQUE `client_public_key` 不是聊天身份公钥**。它是 OPAQUE AKE 公钥；聊天身份用的是单独的 Ed25519/X25519 密钥。
8. **「服务器不见密码」不等于「服务器被攻陷也无法离线猜测」**。服务器 + DB + `oprf_seed` 同时泄露时，弱密码仍可被字典攻击；必须如实告知。
9. **邮箱验证码不能恢复 E2EE 私钥**。忘记密码必须依赖恢复码；没有恢复码就只能放弃历史消息。
10. **不要自研 OPRF、盲化、KDF 组合或用挑战签名替代登录 AKE**。使用标准 OPAQUE 实现与标准 ciphersuite；恢复码重置中的 Ed25519 挑战签名只是应用层「持有旧 key bundle」证明，不替代 OPAQUE 登录认证，但必须绑定用途标签、user_xid、新 record/key bundle 摘要并一次性使用。

## 14. 落地阶段与验收

| 阶段 | 内容 | 验收 |
|---|---|---|
| P4-0 原型 | Go 服务端 + 浏览器 WASM 客户端跑通 RFC 9807 测试向量与注册/登录 | 双端套件一致；KE1/KE2/KE3 互通；错误密码不签发 token |
| P4-1 认证与密钥材料 | `user_e2ee` 迁移、OPAQUE RPC/Gateway 路由、Redis 登录态、恢复码注册/重置 | 换设备登录可解密 key bundle；改密不丢历史；恢复码 + 挑战签名重置后可解密旧消息；无恢复码重置明确警告 |
| P4-2 私聊 MVP | 消息信封、公钥分发、签名、AEAD、seq/防重放、密文存储/转发 | 服务器无明文；篡改/重放被拒；指纹校验可发现公钥替换 |
| P4-3 前向保密 | Double Ratchet / X3DH 预密钥、会话状态、乱序处理 | 完整前向保密与 PCS；安全评审通过；产品文案准确 |

关键失败场景必须测试：

- 错误密码：客户端不发送 KE3，服务端不签发 token；
- 不存在邮箱：返回伪 KE2，不泄露账号存在性；
- 服务器返回被替换的 key bundle：客户端公钥校验失败并拒绝；
- 恢复码错误：recovery_bundle 解密失败，不产生新会话；
- 恢复码正确但挑战签名错误/过期：拒绝保留历史的替换，不得更新 `user_e2ee`；
- 密文篡改/签名错误/重复 seq：接收端拒绝；
- 日志/埋点不得出现密码、`export_key`、私钥、恢复码；
- 模拟服务端重置账号密码（替换 OPAQUE 记录 + 新 `export_key`）：旧 `key_bundle` 仍无法解密，旧历史只能由旧密码或恢复码恢复。

> **已知残余风险（威胁模型 A）**：服务端拿到 DB + `oprf_seed` 后可对弱密码离线字典攻击；OPAQUE 不能消除，只能靠强密码策略 + Argon2id + 密码泄露检测降低。若要求「绝不」可解，需改用高熵主密钥/Passkey/设备密钥，见 §15。

## 15. 待决策事项

| 事项 | 选项/说明 |
|---|---|
| 威胁模型 | **已定 2026-11-01：A —— 服务器可能被入侵/好奇，但不主动修改客户端代码**；恶意前端不在防护范围 |
| 纯密码恢复 vs 高熵主密钥 | 当前选纯密码恢复（威胁模型 A）；若要求「服务端拿到 DB 也绝不能离线猜中」，改用 Passkey/PRF 或设备密钥 + 恢复码，不能纯密码恢复 |
| 客户端加密备份 | 可选：导出 OPAQUE record + key bundle/recovery bundle 密文 + 公钥/版本；服务器丢失数据时使用；是否纳入 P4 待定 |
| 迁移策略 | 一次性切换 vs 双栈过渡；实施前用 `task db-status` 确认数据现状 |
| 恢复码是否强制 | **已定 2026-11-01：采用恢复码方案（B），默认强制生成并确认保存**；是否允许用户跳过仍待产品定，跳过必须明确「重置将丢失历史私聊」 |
| 公钥信任模型 | 指纹/安全码 TOFU；是否引入密钥透明日志（后续） |
| 多设备密钥模型 | 共享 key bundle（MVP）vs 每设备独立密钥 + 设备授权（后续） |
| 消息层前向保密 | **已定 2026-11-01：P4 MVP 用每条消息临时 X25519 + Ed25519 签名（B）；Double Ratchet 作为 P4-3**；若上线前产品要求完整前向保密，再提前 P4-3 |
| OPAQUE 库最终选型 | 需原型互通、安全评审、许可证与维护状态确认后锁定版本 |
| 群聊 | 不在本期；需 sender keys / MLS 等单独设计 |

## 16. 参考规范

- RFC 9807 — The OPAQUE Augmented Password-Authenticated Key Exchange (aPAKE) Protocol
- RFC 9497 — Oblivious Pseudorandom Functions (OPRFs) Using Prime-Order Groups
- RFC 9106 — Argon2 Memory-Hard Function for Password Hashing and Proof-of-Work Applications
- RFC 5869 — HKDF: HMAC-based Extract-and-Expand Key Derivation Function
- RFC 7748 — Elliptic Curves for Security (X25519)
- RFC 8032 — Edwards-Curve Digital Signature Algorithm (Ed25519)
