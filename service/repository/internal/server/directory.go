package server

import (
	"context"

	repositoryv1 "github.com/gangantongxue/GanRAG/api/gen/repository/v1"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/eid"
	"github.com/gangantongxue/GanRAG/pkg/errcode"
)

// CreateDirectory 创建目录（仅 owner / editor；P1 仅 owner，同父目录下名称唯一）
func (h *Handler) CreateDirectory(ctx context.Context, req *repositoryv1.CreateDirectoryRequest) (*repositoryv1.CreateDirectoryResponse, error) {
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
	if req.ParentXid != "" && !eid.Match(req.ParentXid, eid.Directory) {
		return nil, errcode.InvalidArgument("parent_xid 不是合法目录 xid")
	}

	kb, err := h.getWritableKnowledgeBase(ctx, req.KbXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "CreateDirectory", err)
	}
	dir, err := h.st.CreateDirectory(ctx, kb.ID, kb.Xid, req.ParentXid, name)
	if err != nil {
		return nil, errcode.Status(h.log, "CreateDirectory", err)
	}
	return &repositoryv1.CreateDirectoryResponse{Directory: dirToProto(dir)}, nil
}

// UpdateDirectory 目录改名（全量替换，仅 owner / editor）
func (h *Handler) UpdateDirectory(ctx context.Context, req *repositoryv1.UpdateDirectoryRequest) (*repositoryv1.UpdateDirectoryResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if !eid.Match(req.DirXid, eid.Directory) {
		return nil, errcode.InvalidArgument("dir_xid 不是合法目录 xid")
	}
	name, err := validName(req.Name)
	if err != nil {
		return nil, err
	}

	dir, err := h.getWritableDirectory(ctx, req.DirXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "UpdateDirectory", err)
	}
	updated, err := h.st.UpdateDirectory(ctx, dir.ID, name)
	if err != nil {
		return nil, errcode.Status(h.log, "UpdateDirectory", err)
	}
	return &repositoryv1.UpdateDirectoryResponse{Directory: dirToProto(updated)}, nil
}

// MoveDirectory 移动目录（禁止移入自身或子孙目录，仅 owner / editor）
func (h *Handler) MoveDirectory(ctx context.Context, req *repositoryv1.MoveDirectoryRequest) (*repositoryv1.MoveDirectoryResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if !eid.Match(req.DirXid, eid.Directory) {
		return nil, errcode.InvalidArgument("dir_xid 不是合法目录 xid")
	}
	if req.ParentXid != "" && !eid.Match(req.ParentXid, eid.Directory) {
		return nil, errcode.InvalidArgument("parent_xid 不是合法目录 xid")
	}

	dir, err := h.getWritableDirectory(ctx, req.DirXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "MoveDirectory", err)
	}
	moved, err := h.st.MoveDirectory(ctx, dir, req.ParentXid)
	if err != nil {
		return nil, errcode.Status(h.log, "MoveDirectory", err)
	}
	return &repositoryv1.MoveDirectoryResponse{Directory: dirToProto(moved)}, nil
}

// DeleteDirectory 删除目录（级联子目录、文章、附件行与对象，仅 owner / editor）
func (h *Handler) DeleteDirectory(ctx context.Context, req *repositoryv1.DeleteDirectoryRequest) (*repositoryv1.DeleteDirectoryResponse, error) {
	if !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}
	if !eid.Match(req.DirXid, eid.Directory) {
		return nil, errcode.InvalidArgument("dir_xid 不是合法目录 xid")
	}

	dir, err := h.getWritableDirectory(ctx, req.DirXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "DeleteDirectory", err)
	}
	refs, err := h.st.DeleteDirectory(ctx, dir, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "DeleteDirectory", err)
	}
	h.cleanup.Run(ctx, refs)
	return &repositoryv1.DeleteDirectoryResponse{}, nil
}

// GetDirectoryTree 目录树（平铺目录 + 文章清单；私有库仅 owner，公开库匿名可读）
func (h *Handler) GetDirectoryTree(ctx context.Context, req *repositoryv1.GetDirectoryTreeRequest) (*repositoryv1.GetDirectoryTreeResponse, error) {
	if !validKnowledgeBaseXID(req.KbXid) {
		return nil, errcode.InvalidArgument("kb_xid 不是合法知识库 xid")
	}
	if req.UserXid != "" && !validRequiredUserXID(req.UserXid) {
		return nil, errcode.InvalidArgument("user_xid 不是合法用户 xid")
	}

	kb, _, err := h.getReadableKnowledgeBase(ctx, req.KbXid, req.UserXid)
	if err != nil {
		return nil, errcode.Status(h.log, "GetDirectoryTree", err)
	}
	dirs, articles, err := h.st.GetDirectoryTree(ctx, kb.ID)
	if err != nil {
		return nil, errcode.Status(h.log, "GetDirectoryTree", err)
	}
	resp := &repositoryv1.GetDirectoryTreeResponse{
		Directories: make([]*repositoryv1.Directory, 0, len(dirs)),
		Articles:    make([]*repositoryv1.Article, 0, len(articles)),
	}
	for _, d := range dirs {
		resp.Directories = append(resp.Directories, dirToProto(d))
	}
	for _, a := range articles {
		resp.Articles = append(resp.Articles, articleToProto(a))
	}
	return resp, nil
}

// getWritableDirectory 按目录 xid 定位目录并校验写权限；不存在 / 无权限统一 NotFound（防枚举）
func (h *Handler) getWritableDirectory(ctx context.Context, dirXID, userXID string) (*dbmodel.Directory, error) {
	dir, err := h.st.GetDirectoryByXID(ctx, dirXID)
	if err != nil {
		return nil, err
	}
	if _, err := h.getWritableKnowledgeBase(ctx, dir.KbXid, userXID); err != nil {
		return nil, err
	}
	return dir, nil
}

// dirToProto 数据库模型转 proto
func dirToProto(d *dbmodel.Directory) *repositoryv1.Directory {
	return &repositoryv1.Directory{
		Xid:           d.Xid,
		KbXid:         d.KbXid,
		ParentXid:     d.ParentXid,
		Name:          d.Name,
		CreatedAtUnix: d.CreatedAt.Unix(),
		UpdatedAtUnix: d.UpdatedAt.Unix(),
	}
}

// articleToProto 数据库模型转 proto（正文与附件清单另经文章接口获取）
func articleToProto(a *dbmodel.Article) *repositoryv1.Article {
	return &repositoryv1.Article{
		Xid:           a.Xid,
		KbXid:         a.KbXid,
		DirXid:        a.DirXid,
		Type:          a.Type,
		Title:         a.Title,
		CreatedByXid:  a.CreatedByXid,
		CreatedAtUnix: a.CreatedAt.Unix(),
		UpdatedAtUnix: a.UpdatedAt.Unix(),
	}
}
