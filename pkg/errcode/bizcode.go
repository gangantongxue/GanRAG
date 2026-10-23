package errcode

// 业务错误码常量：代码内唯一号源（5 位 HHNNN，语义见 README「业务码号段」）
//
// 【强制约定】声明业务错误一律引用本文件常量，禁止裸数字（含 Gateway / 中间件
// 的兜底码），防止号表漂移与撞号。新增错误码流程：
//  1. 在对应服务分段内取下一个序号声明常量（不得跨段占用、不得复用已删号码）
//  2. 用 errcode.New(常量, codes.X, "消息") 声明哨兵错误
//  3. 同步 README 号表与 TestCodeValues 清单（撞号防护）
const (
	// ---- 通用兜底（序号 00，GenericCode 按 HTTP 码×100 派生，两者必须一致）----

	CodeInvalidArgument    = 40000 // 参数不合法
	CodeUnauthenticated    = 40100 // 未认证
	CodePermissionDenied   = 40300 // 无权限
	CodeNotFound           = 40400 // 不存在
	CodeConflict           = 40900 // 冲突（AlreadyExists / Aborted）
	CodeFailedPrecondition = 41200 // 前置条件失败
	CodeResourceExhausted  = 42900 // 限频
	CodeInternal           = 50000 // 内部错误
	CodeUnavailable        = 50200 // 下游不可用
)

const (
	// ---- user 服务（01-29）----

	CodeUserInvalidCredentials   = 40101 // 邮箱或密码错误（登录防枚举）
	CodeUserTokenInvalid         = 40102 // refresh token 无效
	CodeUserTokenExpired         = 40103 // refresh token 已过期
	CodeUserOldPasswordWrong     = 40104 // 旧密码错误
	CodeUserAuthFailed           = 40105 // 认证失败
	CodeUserEmailCodeInvalid     = 40106 // 验证码无效或已过期
	CodeUserNotFound             = 40401 // 用户不存在
	CodeUserEmailTaken           = 40901 // 邮箱已注册
	CodeUserDisabled             = 41201 // 账号已被禁用
	CodeUserEmailCodeRateLimited = 42901 // 发送过于频繁
)

const (
	// ---- repository 服务（30-59）----

	CodeRepoDirectoryMoveIntoSelf = 40030 // 不能移入自身或子孙目录
	CodeRepoKBNotFound            = 40430 // 知识库不存在
	CodeRepoDirNotFound           = 40431 // 目录不存在
	CodeRepoKBNameTaken           = 40930 // 知识库名称已存在
	CodeRepoDirNameTaken          = 40931 // 同级目录名称已存在
	CodeRepoChecksumMismatch      = 50030 // sha256 校验失败
)

const (
	// ---- vector-store 服务（60-89）----

	CodeVectorCollectionNotFound = 40460 // 集合不存在
	CodeVectorDocumentNotFound   = 40461 // 文档不存在
	CodeVectorCollectionExists   = 40960 // 集合已存在
)

const (
	// ---- 共享包（90-99）----

	CodeTaskNotFound = 40490 // 任务不存在（pkg/asynctask）
)
