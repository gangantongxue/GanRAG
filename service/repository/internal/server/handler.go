package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"mime"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"

	repositoryv1 "github.com/gangantongxue/GanRAG/api/gen/repository/v1"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/eid"
	"github.com/gangantongxue/GanRAG/pkg/errcode"
	"github.com/gangantongxue/GanRAG/service/repository/internal/store"
)

const (
	knowledgeBaseNameMaxLen = 100
	knowledgeBaseDescMaxLen = 500
	originalNameMaxLen      = 255
	contentTypeMaxLen       = 100
	sha256HexLen            = 64
)

// objectPutter Garage 写入（*objectstore.Store 实现）
type objectPutter interface {
	Put(ctx context.Context, key string, data []byte, contentType string, metadata map[string]string) error
}

// cleanupRunner 事务提交后的对象清理（*cleanup.Runner 实现）
type cleanupRunner interface {
	Run(ctx context.Context, refs []store.ObjectRef)
}

// Handler 实现 repositoryv1.RepositoryServiceServer（阶段一、二a：知识库 + 目录树 + 整段上传）
type Handler struct {
	repositoryv1.UnimplementedRepositoryServiceServer
	st           *store.Store
	objects      objectPutter
	cleanup      cleanupRunner
	log          *slog.Logger
	maxWholeSize int64
}

// NewHandler 创建 gRPC handler
func NewHandler(st *store.Store, objects objectPutter, cleaner cleanupRunner, log *slog.Logger, maxWholeSize int64) *Handler {
	return &Handler{st: st, objects: objects, cleanup: cleaner, log: log, maxWholeSize: maxWholeSize}
}

// CreateKnowledgeBase 创建知识库
func (h *Handler) CreateKnowledgeBase(ctx context.Context, req *repositoryv1.CreateKnowledgeBaseRequest) (*repositoryv1.CreateKnowledgeBaseResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	name, err := validName(req.Name)
	if err != nil {
		return nil, err
	}
	if err := validKnowledgeBaseDescription(req.Description); err != nil {
		return nil, err
	}
	if !validVisibility(req.Visibility) {
		return nil, errcode.InvalidArgument("visibility 仅支持 0 私有 / 1 公开")
	}

	kb, err := h.st.CreateKnowledgeBase(ctx, req.UserXid, name, req.Description, req.Visibility)
	if err != nil {
		return nil, errcode.Status(h.log, "CreateKnowledgeBase", err)
	}
	return &repositoryv1.CreateKnowledgeBaseResponse{KnowledgeBase: kbToProto(kb)}, nil
}

// GetKnowledgeBase 查询知识库（私有库仅 owner；公开库匿名可读）
func (h *Handler) GetKnowledgeBase(ctx context.Context, req *repositoryv1.GetKnowledgeBaseRequest) (*repositoryv1.GetKnowledgeBaseResponse, error) {
	if !validKnowledgeBaseXID(req.KbXid) {
		return nil, errcode.InvalidArgument("kb_xid 不是合法知识库 xid")
	}
	if req.UserXid != "" && !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}

	kb, canEdit, err := h.getReadableKnowledgeBase(ctx, req.KbXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "GetKnowledgeBase", err)
	}
	return &repositoryv1.GetKnowledgeBaseResponse{KnowledgeBase: kbToProto(kb), CanEdit: canEdit}, nil
}

// ListKnowledgeBases 分页列表
func (h *Handler) ListKnowledgeBases(ctx context.Context, req *repositoryv1.ListKnowledgeBasesRequest) (*repositoryv1.ListKnowledgeBasesResponse, error) {
	if req.Scope < store.ScopeOwned || req.Scope > store.ScopeAllVisible {
		return nil, errcode.InvalidArgument("scope 仅支持 1-4")
	}
	if req.UserXid != "" && !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if (req.Scope == store.ScopeOwned || req.Scope == store.ScopeParticipated) && req.UserXid == "" {
		return nil, errcode.InvalidArgument("scope 1/2 需要登录用户")
	}

	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	rows, total, err := h.st.ListKnowledgeBases(ctx, req.UserXid, req.Scope, page, pageSize)
	if err != nil {
		return nil, errcode.Status(h.log, "ListKnowledgeBases", err)
	}
	items := make([]*repositoryv1.KnowledgeBase, 0, len(rows))
	for _, kb := range rows {
		items = append(items, kbToProto(kb))
	}
	return &repositoryv1.ListKnowledgeBasesResponse{Items: items, Total: int32(total)}, nil
}

// UpdateKnowledgeBase 更新知识库（仅 owner）
func (h *Handler) UpdateKnowledgeBase(ctx context.Context, req *repositoryv1.UpdateKnowledgeBaseRequest) (*repositoryv1.UpdateKnowledgeBaseResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if !validKnowledgeBaseXID(req.KbXid) {
		return nil, errcode.InvalidArgument("kb_xid 不是合法知识库 xid")
	}
	name, err := validName(req.Name)
	if err != nil {
		return nil, err
	}
	if err := validKnowledgeBaseDescription(req.Description); err != nil {
		return nil, err
	}
	if !validVisibility(req.Visibility) {
		return nil, errcode.InvalidArgument("visibility 仅支持 0 私有 / 1 公开")
	}

	kb, err := h.getWritableKnowledgeBase(ctx, req.KbXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "UpdateKnowledgeBase", err)
	}
	updated, err := h.st.UpdateKnowledgeBase(ctx, kb.ID, name, req.Description, req.Visibility)
	if err != nil {
		return nil, errcode.Status(h.log, "UpdateKnowledgeBase", err)
	}
	return &repositoryv1.UpdateKnowledgeBaseResponse{KnowledgeBase: kbToProto(updated)}, nil
}

// DeleteKnowledgeBase 删除知识库（仅 owner；级联删除 + 对象清理任务同事务）
func (h *Handler) DeleteKnowledgeBase(ctx context.Context, req *repositoryv1.DeleteKnowledgeBaseRequest) (*repositoryv1.DeleteKnowledgeBaseResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if !validKnowledgeBaseXID(req.KbXid) {
		return nil, errcode.InvalidArgument("kb_xid 不是合法知识库 xid")
	}

	kb, err := h.getWritableKnowledgeBase(ctx, req.KbXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "DeleteKnowledgeBase", err)
	}
	refs, err := h.st.DeleteKnowledgeBase(ctx, kb.ID, kb.Xid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "DeleteKnowledgeBase", err)
	}
	h.cleanup.Run(ctx, refs)
	return &repositoryv1.DeleteKnowledgeBaseResponse{}, nil
}

// UploadWhole 整段上传小文件（≤ 配置上限，默认 2MB）
func (h *Handler) UploadWhole(ctx context.Context, req *repositoryv1.UploadWholeRequest) (*repositoryv1.UploadWholeResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if int64(len(req.Data)) > h.maxWholeSize {
		return nil, errcode.InvalidArgument("content too large, use chunked upload")
	}
	contentType, err := validContentType(req.ContentType)
	if err != nil {
		return nil, err
	}
	originalName, err := validOriginalName(req.OriginalName)
	if err != nil {
		return nil, err
	}
	declared, err := validSHA256(req.Sha256)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(req.Data)
	actual := hex.EncodeToString(sum[:])
	if actual != declared {
		return nil, errcode.New(errcode.CodeRepoChecksumMismatch, codes.DataLoss, "sha256 校验失败")
	}

	objectXID := eid.New(eid.Object)
	metadata := map[string]string{
		"sha256":            actual,
		"original-name-b64": base64.RawURLEncoding.EncodeToString([]byte(originalName)),
		"uploader-xid":      req.UserXid,
	}
	if err := h.objects.Put(ctx, "o/"+objectXID, req.Data, contentType, metadata); err != nil {
		return nil, errcode.Status(h.log, "UploadWhole", err)
	}

	return &repositoryv1.UploadWholeResponse{Result: &repositoryv1.UploadResult{
		ObjectXid:   objectXID,
		Size:        uint64(len(req.Data)),
		Sha256:      actual,
		ContentType: contentType,
	}}, nil
}

// getReadableKnowledgeBase 读取权限：私有库仅 owner，公开库匿名可读；无权限统一 NotFound
func (h *Handler) getReadableKnowledgeBase(ctx context.Context, kbXID, userXID string) (*dbmodel.KnowledgeBase, bool, error) {
	kb, err := h.st.GetKnowledgeBaseByXID(ctx, kbXID)
	if err != nil {
		return nil, false, err
	}
	canEdit := userXID != "" && kb.OwnerXid == userXID
	if kb.Visibility == 0 && !canEdit {
		return nil, false, store.ErrKnowledgeBaseNotFound
	}
	return kb, canEdit, nil
}

// getWritableKnowledgeBase 写入权限：仅 owner；不匹配统一 NotFound（防枚举）
func (h *Handler) getWritableKnowledgeBase(ctx context.Context, kbXID, userXID string) (*dbmodel.KnowledgeBase, error) {
	kb, err := h.st.GetKnowledgeBaseByXID(ctx, kbXID)
	if err != nil {
		return nil, err
	}
	if kb.OwnerXid != userXID {
		return nil, store.ErrKnowledgeBaseNotFound
	}
	return kb, nil
}

// kbToProto 数据库模型转 proto
func kbToProto(kb *dbmodel.KnowledgeBase) *repositoryv1.KnowledgeBase {
	return &repositoryv1.KnowledgeBase{
		Xid:           kb.Xid,
		OwnerXid:      kb.OwnerXid,
		Name:          kb.Name,
		Description:   kb.Description,
		Visibility:    kb.Visibility,
		ArticleCount:  kb.ArticleCount,
		StarCount:     kb.StarCount,
		CreatedAtUnix: kb.CreatedAt.Unix(),
		UpdatedAtUnix: kb.UpdatedAt.Unix(),
	}
}

// validRequiredUserXID 校验必填用户 xid
func validRequiredUserXID(s string) bool {
	return eid.Match(s, eid.User)
}

// validKnowledgeBaseXID 校验知识库 xid
func validKnowledgeBaseXID(s string) bool {
	return eid.Match(s, eid.KnowledgeBase)
}

// validName 去首尾空白并校验 1-100 字符（知识库名 / 目录名同规则）
func validName(s string) (string, error) {
	name := strings.TrimSpace(s)
	n := utf8.RuneCountInString(name)
	if n < 1 || n > knowledgeBaseNameMaxLen {
		return "", errcode.InvalidArgument("名称长度应为 1-100 字符")
	}
	return name, nil
}

// validKnowledgeBaseDescription 校验描述 ≤500 字符
func validKnowledgeBaseDescription(s string) error {
	if utf8.RuneCountInString(s) > knowledgeBaseDescMaxLen {
		return errcode.InvalidArgument("知识库描述不能超过 500 字符")
	}
	return nil
}

// validVisibility 校验可见性 0/1
func validVisibility(v int32) bool {
	return v == 0 || v == 1
}

// validContentType 校验并规范化 MIME 类型；空值默认 application/octet-stream
func validContentType(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "application/octet-stream", nil
	}
	mediaType, _, err := mime.ParseMediaType(s)
	if err != nil || mediaType == "" || len(mediaType) > contentTypeMaxLen {
		return "", errcode.InvalidArgument("content_type 不是合法 MIME 类型")
	}
	return mediaType, nil
}

// validOriginalName 校验原始文件名长度（仅展示用）
func validOriginalName(s string) (string, error) {
	if utf8.RuneCountInString(s) > originalNameMaxLen {
		return "", errcode.InvalidArgument("original_name 不能超过 255 字符")
	}
	return s, nil
}

// validSHA256 校验并归一化 64 位十六进制 sha256
func validSHA256(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != sha256HexLen {
		return "", errcode.InvalidArgument("sha256 必须是 64 位十六进制")
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != sha256.Size {
		return "", errcode.InvalidArgument("sha256 必须是 64 位十六进制")
	}
	return s, nil
}
