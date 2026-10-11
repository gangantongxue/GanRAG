// Package server 提供 File Store 的 gRPC 服务端实现
package server

import (
	"context"
	"log/slog"

	filestorev1 "github.com/gangantongxue/GanRAG/api/gen/file-store/v1"
	"github.com/gangantongxue/GanRAG/pkg/errcode"
	"github.com/gangantongxue/GanRAG/service/file-store/internal/store"
)

// Handler 实现 filestorev1.FileStoreServer 接口
type Handler struct {
	filestorev1.UnimplementedFileStoreServer

	store *store.Store // 对象存储
	log   *slog.Logger // 日志
}

// NewHandler 创建 gRPC handler
//
// 参数：
//   - st: 对象存储实例
//   - log: 日志实例
//
// 返回：
//   - *Handler: handler 实例
func NewHandler(st *store.Store, log *slog.Logger) *Handler {
	return &Handler{store: st, log: log}
}

// Upload 上传文件；同 bucket+key 已存在时覆盖旧文件并更新元数据
func (h *Handler) Upload(_ context.Context, req *filestorev1.UploadRequest) (*filestorev1.UploadResponse, error) {
	md, err := h.store.Upload(req.GetBucket(), req.GetKey(), req.GetData(), req.GetContentType())
	if err != nil {
		return nil, h.toStatus("Upload", err)
	}
	return &filestorev1.UploadResponse{Metadata: toProtoMetadata(md)}, nil
}

// Download 下载文件内容及其元数据
func (h *Handler) Download(_ context.Context, req *filestorev1.DownloadRequest) (*filestorev1.DownloadResponse, error) {
	data, md, err := h.store.Download(req.GetBucket(), req.GetKey())
	if err != nil {
		return nil, h.toStatus("Download", err)
	}
	return &filestorev1.DownloadResponse{Data: data, Metadata: toProtoMetadata(md)}, nil
}

// Delete 删除文件及其元数据
func (h *Handler) Delete(_ context.Context, req *filestorev1.DeleteRequest) (*filestorev1.DeleteResponse, error) {
	if err := h.store.Delete(req.GetBucket(), req.GetKey()); err != nil {
		return nil, h.toStatus("Delete", err)
	}
	return &filestorev1.DeleteResponse{}, nil
}

// GetMetadata 查询单个文件的元数据
func (h *Handler) GetMetadata(_ context.Context, req *filestorev1.GetMetadataRequest) (*filestorev1.GetMetadataResponse, error) {
	md, err := h.store.GetMetadata(req.GetBucket(), req.GetKey())
	if err != nil {
		return nil, h.toStatus("GetMetadata", err)
	}
	return &filestorev1.GetMetadataResponse{Metadata: toProtoMetadata(md)}, nil
}

// List 列出 bucket 下所有文件的元数据（bucket 不存在返回空列表）
func (h *Handler) List(_ context.Context, req *filestorev1.ListRequest) (*filestorev1.ListResponse, error) {
	files, err := h.store.List(req.GetBucket())
	if err != nil {
		return nil, h.toStatus("List", err)
	}
	pbFiles := make([]*filestorev1.FileMetadata, 0, len(files))
	for i := range files {
		pbFiles = append(pbFiles, toProtoMetadata(&files[i]))
	}
	return &filestorev1.ListResponse{Files: pbFiles}, nil
}

// toStatus 将 store 层错误映射为 gRPC 状态错误
//
// 统一委托 pkg/errcode.Status：自带状态码的哨兵错误按其码转换，
// 未知错误记录日志后统一返回 Internal，避免暴露内部错误细节。
// 消息体超过服务端 16MB 接收上限的情况由 gRPC 框架直接返回 ResourceExhausted，
// 不会进入 handler。
//
// 参数：
//   - method: 出错的方法名，用于日志定位
//   - err: store 层返回的错误
//
// 返回：
//   - error: gRPC 状态错误
func (h *Handler) toStatus(method string, err error) error {
	return errcode.Status(h.log, method, err)
}

// toProtoMetadata 将 store.Metadata 转换为 proto FileMetadata
func toProtoMetadata(md *store.Metadata) *filestorev1.FileMetadata {
	return &filestorev1.FileMetadata{
		Bucket:         md.Bucket,
		Key:            md.Key,
		Size:           md.Size,
		ContentType:    md.ContentType,
		Sha256:         md.SHA256,
		UploadedAtUnix: md.UploadedAtUnix,
	}
}
