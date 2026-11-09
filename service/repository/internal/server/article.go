package server

import (
	"context"
	"encoding/base64"
	"errors"

	repositoryv1 "github.com/gangantongxue/GanRAG/api/gen/repository/v1"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/eid"
	"github.com/gangantongxue/GanRAG/pkg/errcode"
	"github.com/gangantongxue/GanRAG/service/repository/internal/objectstore"
	"github.com/gangantongxue/GanRAG/service/repository/internal/store"
)

// CreateArticle 创建文章（正文与附件先经上传接口得 object_xid，此处只引用）
//
// 正文可暂为空（content_object_xid 空串），后续 UpdateArticle 补充。
// 错误码：InvalidArgument 参数不合法；NotFound 知识库/目录不存在或无写权限。
func (h *Handler) CreateArticle(ctx context.Context, req *repositoryv1.CreateArticleRequest) (*repositoryv1.CreateArticleResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if !validKnowledgeBaseXID(req.KbXid) {
		return nil, errcode.InvalidArgument("kb_xid 不是合法知识库 xid")
	}
	if req.DirXid != "" && !eid.Match(req.DirXid, eid.Directory) {
		return nil, errcode.InvalidArgument("dir_xid 不是合法目录 xid")
	}
	if !validArticleType(req.Type) {
		return nil, errcode.InvalidArgument("type 仅支持 1 md / 2 txt")
	}
	title, err := validTitle(req.Title)
	if err != nil {
		return nil, err
	}
	if req.ContentObjectXid != "" && !eid.Match(req.ContentObjectXid, eid.Object) {
		return nil, errcode.InvalidArgument("content_object_xid 不是合法对象 xid")
	}
	atts, err := h.buildAttachmentInputs(ctx, req.Attachments)
	if err != nil {
		return nil, err
	}

	kb, err := h.getWritableKnowledgeBase(ctx, req.KbXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "CreateArticle", err)
	}
	art, err := h.st.CreateArticle(ctx, kb.ID, kb.Xid, req.DirXid, req.Type, title, req.ContentObjectXid, req.UserXid, atts)
	if err != nil {
		return nil, errcode.Status(h.log, "CreateArticle", err)
	}
	return &repositoryv1.CreateArticleResponse{Article: articleToProto(art)}, nil
}

// GetArticle 文章元信息 + 附件清单（正文经 GetArticleContent 另取）
//
// 读取权限同知识库：私有库仅 owner，公开库匿名可读；无权限统一 NotFound。
func (h *Handler) GetArticle(ctx context.Context, req *repositoryv1.GetArticleRequest) (*repositoryv1.GetArticleResponse, error) {
	if !eid.Match(req.ArticleXid, eid.Article) {
		return nil, errcode.InvalidArgument("article_xid 不是合法文章 xid")
	}
	if req.UserXid != "" && !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}

	art, err := h.st.GetArticleByXID(ctx, req.ArticleXid)
	if err != nil {
		return nil, errcode.Status(h.log, "GetArticle", err)
	}
	_, canEdit, err := h.getReadableKnowledgeBase(ctx, art.KbXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "GetArticle", err)
	}
	atts, err := h.st.ListAttachments(ctx, art.ID)
	if err != nil {
		return nil, errcode.Status(h.log, "GetArticle", err)
	}
	resp := &repositoryv1.GetArticleResponse{
		Article:     articleToProto(art),
		CanEdit:     canEdit,
		Attachments: make([]*repositoryv1.Attachment, 0, len(atts)),
	}
	for _, a := range atts {
		resp.Attachments = append(resp.Attachments, attachmentToProto(a))
	}
	return resp, nil
}

// UpdateArticle 更新文章（title/dir 全量替换；content_object_xid 空串 = 保留原正文；
// attachments 全量重建 kind=2，空 = 清空，孤儿对象即时清理）
//
// dir_xid 全量替换语义：空串 = 移到根。错误码：InvalidArgument 参数不合法；
// NotFound 文章/目录不存在或无写权限；AlreadyExists 附件相对路径冲突。
func (h *Handler) UpdateArticle(ctx context.Context, req *repositoryv1.UpdateArticleRequest) (*repositoryv1.UpdateArticleResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if !eid.Match(req.ArticleXid, eid.Article) {
		return nil, errcode.InvalidArgument("article_xid 不是合法文章 xid")
	}
	if req.DirXid != "" && !eid.Match(req.DirXid, eid.Directory) {
		return nil, errcode.InvalidArgument("dir_xid 不是合法目录 xid")
	}
	title, err := validTitle(req.Title)
	if err != nil {
		return nil, err
	}
	if req.ContentObjectXid != "" && !eid.Match(req.ContentObjectXid, eid.Object) {
		return nil, errcode.InvalidArgument("content_object_xid 不是合法对象 xid")
	}
	atts, err := h.buildAttachmentInputs(ctx, req.Attachments)
	if err != nil {
		return nil, err
	}

	art, err := h.getWritableArticle(ctx, req.ArticleXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "UpdateArticle", err)
	}
	updated, refs, err := h.st.UpdateArticle(ctx, art, req.DirXid, title, req.ContentObjectXid, atts)
	if err != nil {
		return nil, errcode.Status(h.log, "UpdateArticle", err)
	}
	h.cleanup.Run(ctx, refs)
	return &repositoryv1.UpdateArticleResponse{Article: articleToProto(updated)}, nil
}

// DeleteArticle 删除文章（级联附件行与对象，仅 owner）
//
// 对象删除走公共任务 outbox + 事务后尽力删，失败可重试。
func (h *Handler) DeleteArticle(ctx context.Context, req *repositoryv1.DeleteArticleRequest) (*repositoryv1.DeleteArticleResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if !eid.Match(req.ArticleXid, eid.Article) {
		return nil, errcode.InvalidArgument("article_xid 不是合法文章 xid")
	}

	art, err := h.getWritableArticle(ctx, req.ArticleXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "DeleteArticle", err)
	}
	refs, err := h.st.DeleteArticle(ctx, art, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "DeleteArticle", err)
	}
	h.cleanup.Run(ctx, refs)
	return &repositoryv1.DeleteArticleResponse{}, nil
}

// getWritableArticle 按文章 xid 定位文章并校验写权限；不存在 / 无权限统一 NotFound（防枚举）
func (h *Handler) getWritableArticle(ctx context.Context, articleXID, userXID string) (*dbmodel.Article, error) {
	art, err := h.st.GetArticleByXID(ctx, articleXID)
	if err != nil {
		return nil, err
	}
	if _, err := h.getWritableKnowledgeBase(ctx, art.KbXid, userXID); err != nil {
		return nil, err
	}
	return art, nil
}

// buildAttachmentInputs 校验附件引用并经对象 HEAD 回填元信息
//
// AttachmentRef 只传 relative_path + object_xid，attachments 行的
// original_name/content_type/size/sha256 从对象元数据回填（上传时已写入）。
func (h *Handler) buildAttachmentInputs(ctx context.Context, refs []*repositoryv1.AttachmentRef) ([]store.AttachmentInput, error) {
	out := make([]store.AttachmentInput, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		path, err := normalizeRelativePath(ref.RelativePath)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[path]; dup {
			return nil, errcode.InvalidArgument("attachments relative_path 重复: " + path)
		}
		seen[path] = struct{}{}
		if !eid.Match(ref.ObjectXid, eid.Object) {
			return nil, errcode.InvalidArgument("attachments object_xid 不是合法对象 xid")
		}

		info, err := h.objects.Head(ctx, "o/"+ref.ObjectXid)
		if err != nil {
			if errors.Is(err, objectstore.ErrObjectNotFound) {
				return nil, errcode.InvalidArgument("附件对象不存在: " + ref.ObjectXid)
			}
			return nil, errcode.Status(h.log, "BuildAttachments/Head", err)
		}
		out = append(out, store.AttachmentInput{
			RelativePath: path,
			ObjectXID:    ref.ObjectXid,
			OriginalName: originalNameFromMeta(info.Metadata),
			ContentType:  info.ContentType,
			Size:         info.Size,
			Sha256:       info.Metadata["sha256"],
		})
	}
	return out, nil
}

// originalNameFromMeta 从对象元数据解出原始文件名（UploadWhole 以 base64 存入）；缺失回退空串
func originalNameFromMeta(meta map[string]string) string {
	raw, err := base64.RawURLEncoding.DecodeString(meta["original-name-b64"])
	if err != nil {
		return ""
	}
	return string(raw)
}

// attachmentToProto 附件库行转 proto
func attachmentToProto(a *dbmodel.Attachment) *repositoryv1.Attachment {
	return &repositoryv1.Attachment{
		Xid:           a.Xid,
		RelativePath:  a.RelativePath,
		ObjectXid:     a.ObjectXid,
		OriginalName:  a.OriginalName,
		ContentType:   a.ContentType,
		Size:          uint64(a.Size),
		CreatedAtUnix: a.CreatedAt.Unix(),
	}
}
