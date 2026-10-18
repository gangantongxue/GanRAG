// Package eid 封装全项目业务主键（entity id）的生成与校验：业务主键形如
// <前缀>_<20位XID>（如 usr_4f3k2j...），前缀按实体类型区分，防止不同实体
// 的 ID 串用；底层 XID 由 github.com/rs/xid 生成。
//
// 【强制约定】业务主键统一使用本包，不要直接使用 github.com/rs/xid：
//
//  1. 对外展示实体一律用 New(eid.<类型>) 生成业务主键入库（列 xid VARCHAR(32) 存全串）；
//  2. 对外接口 / URL / 前端只出业务主键，数据库自增 id 不出服务；
//  3. 入参校验用 Valid / Match（Match 额外校验前缀，防跨实体串类型），
//     校验失败的对外提示由调用方用 errcode.InvalidArgument 构造；
//  4. 表间逻辑引用（关联字段）一律存数据库自增主键，不存业务主键。
package eid

import (
	"strings"

	gxid "github.com/rs/xid"
)

// Type 实体类型（即业务主键前缀）
type Type string

// 各实体业务主键前缀（3 字母小写 + _）
const (
	User          Type = "usr" // 用户
	Session       Type = "ses" // 登录会话
	Device        Type = "dev" // 登录设备（sessions.device_id 取值）
	KnowledgeBase Type = "kb"  // 知识库
	Directory     Type = "dir" // 目录
	Article       Type = "art" // 文章
	Attachment    Type = "att" // 附件
	ChatSession   Type = "chs" // 聊天会话
	ChatMessage   Type = "msg" // 聊天消息
	Object        Type = "obj" // 对象存储对象（Garage key：o/<obj_xid>）
	Upload        Type = "upl" // 上传会话（分片上传 upload_id，见 api/proto/repository/v1）
	Task          Type = "tsk" // 公共异步任务/失败记录（ganrag_common.async_tasks）
)

// maxLen 业务主键全串长度上限（库列 VARCHAR(32)）；最长 4 + 20 = 24，留扩展余量
const maxLen = 32

// registeredTypes 已注册的实体类型（New 生成与 Valid 校验的白名单）
var registeredTypes = map[Type]struct{}{
	User: {}, Session: {}, Device: {}, KnowledgeBase: {},
	Directory: {}, Article: {}, Attachment: {},
	ChatSession: {}, ChatMessage: {}, Object: {}, Upload: {}, Task: {},
}

// New 生成指定实体类型的业务主键，形如 usr_<20位XID>
//
// 参数：
//   - t: 实体类型（前缀），必须是本包注册的常量
//
// 返回：
//   - string: <前缀>_<20位XID> 全串（入库与对外均使用该值）
//
// 未注册类型属编程错误，直接 panic：宁可崩在开发期，也不要生成无法解析的 ID。
func New(t Type) string {
	if _, ok := registeredTypes[t]; !ok {
		panic("eid: 未注册的实体类型 " + string(t))
	}
	return string(t) + "_" + gxid.New().String()
}

// Valid 校验业务主键整体格式：前缀已注册 + XID 段合法 + 长度不超上限
//
// 参数：
//   - s: 待校验的业务主键全串
//
// 返回：
//   - bool: 合法返回 true
func Valid(s string) bool {
	_, ok := parse(s)
	return ok
}

// Match 校验业务主键格式且前缀必须等于指定类型（入参处防跨实体串用）
//
// 参数：
//   - s: 待校验的业务主键全串
//   - t: 期望的实体类型
//
// 返回：
//   - bool: 格式合法且类型匹配返回 true
func Match(s string, t Type) bool {
	got, ok := parse(s)
	return ok && got == t
}

// parse 拆出前缀并校验 XID 段
//
// 返回：
//   - Type: 解析出的实体类型（非法时为空）
//   - bool: 合法返回 true
func parse(s string) (Type, bool) {
	if s == "" || len(s) > maxLen {
		return "", false
	}
	p, raw, found := strings.Cut(s, "_")
	if !found {
		return "", false
	}
	t := Type(p)
	if _, ok := registeredTypes[t]; !ok {
		return "", false
	}
	// XID 段为 20 位小写 base32，交给 rs/xid 校验（含非法字符、长度不符）
	if _, err := gxid.FromString(raw); err != nil {
		return "", false
	}
	return t, true
}
