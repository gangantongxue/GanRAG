// Package server 提供 Vector Store 的 gRPC 服务端实现
package server

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vectorstorev1 "github.com/gangantongxue/GanRAG/api/gen/vector-store/v1"
	"github.com/gangantongxue/GanRAG/service/vector-store/internal/store"
)

// Handler 实现 vectorstorev1.VectorStoreServer 接口
type Handler struct {
	vectorstorev1.UnimplementedVectorStoreServer

	store *store.Store // 向量存储
	log   *slog.Logger // 日志
}

// NewHandler 创建 gRPC handler
//
// 参数：
//   - st: 向量存储实例
//   - log: 日志实例
//
// 返回：
//   - *Handler: handler 实例
func NewHandler(st *store.Store, log *slog.Logger) *Handler {
	return &Handler{store: st, log: log}
}

// CreateCollection 创建集合
func (h *Handler) CreateCollection(_ context.Context, req *vectorstorev1.CreateCollectionRequest) (*vectorstorev1.CreateCollectionResponse, error) {
	if err := h.store.CreateCollection(req.GetName(), req.GetMetadata()); err != nil {
		return nil, h.toStatus("CreateCollection", err)
	}
	return &vectorstorev1.CreateCollectionResponse{}, nil
}

// DeleteCollection 删除集合及其全部文档
func (h *Handler) DeleteCollection(_ context.Context, req *vectorstorev1.DeleteCollectionRequest) (*vectorstorev1.DeleteCollectionResponse, error) {
	if err := h.store.DeleteCollection(req.GetName()); err != nil {
		return nil, h.toStatus("DeleteCollection", err)
	}
	return &vectorstorev1.DeleteCollectionResponse{}, nil
}

// ListCollections 列出所有集合及文档数量
func (h *Handler) ListCollections(_ context.Context, _ *vectorstorev1.ListCollectionsRequest) (*vectorstorev1.ListCollectionsResponse, error) {
	infos := h.store.ListCollections()
	collections := make([]*vectorstorev1.Collection, 0, len(infos))
	for _, info := range infos {
		collections = append(collections, &vectorstorev1.Collection{
			Name:  info.Name,
			Count: int64(info.Count),
		})
	}
	return &vectorstorev1.ListCollectionsResponse{Collections: collections}, nil
}

// Write 批量写入文档
func (h *Handler) Write(ctx context.Context, req *vectorstorev1.WriteRequest) (*vectorstorev1.WriteResponse, error) {
	docs := make([]store.Document, 0, len(req.GetDocuments()))
	for _, doc := range req.GetDocuments() {
		docs = append(docs, toStoreDocument(doc))
	}

	written, err := h.store.Write(ctx, req.GetCollection(), docs, req.GetCreateIfMissing())
	if err != nil {
		return nil, h.toStatus("Write", err)
	}
	return &vectorstorev1.WriteResponse{Written: int64(written)}, nil
}

// Search 相似度检索
func (h *Handler) Search(ctx context.Context, req *vectorstorev1.SearchRequest) (*vectorstorev1.SearchResponse, error) {
	results, err := h.store.Search(
		ctx,
		req.GetCollection(),
		req.GetQueryEmbedding(),
		int(req.GetTopK()),
		req.GetWhere(),
		req.GetWhereDocument(),
	)
	if err != nil {
		return nil, h.toStatus("Search", err)
	}

	pbResults := make([]*vectorstorev1.SearchResult, 0, len(results))
	for _, result := range results {
		pbResults = append(pbResults, toProtoResult(result))
	}
	return &vectorstorev1.SearchResponse{Results: pbResults}, nil
}

// Delete 删除集合中的文档
func (h *Handler) Delete(ctx context.Context, req *vectorstorev1.DeleteRequest) (*vectorstorev1.DeleteResponse, error) {
	if err := h.store.Delete(ctx, req.GetCollection(), req.GetIds(), req.GetWhere(), req.GetWhereDocument()); err != nil {
		return nil, h.toStatus("Delete", err)
	}
	return &vectorstorev1.DeleteResponse{}, nil
}

// GetByID 按文档 ID 查询
func (h *Handler) GetByID(ctx context.Context, req *vectorstorev1.GetByIDRequest) (*vectorstorev1.GetByIDResponse, error) {
	doc, err := h.store.GetByID(ctx, req.GetCollection(), req.GetId())
	if err != nil {
		return nil, h.toStatus("GetByID", err)
	}
	return &vectorstorev1.GetByIDResponse{Document: toProtoDocument(doc)}, nil
}

// toStatus 将 store 层错误映射为 gRPC 状态错误
//
// 已知的哨兵错误映射为对应状态码，未知错误记录日志后统一返回 Internal，
// 避免把内部错误细节暴露给调用方。
//
// 参数：
//   - method: 出错的方法名，用于日志定位
//   - err: store 层返回的错误
//
// 返回：
//   - error: gRPC 状态错误
func (h *Handler) toStatus(method string, err error) error {
	switch {
	case errors.Is(err, store.ErrCollectionNotFound), errors.Is(err, store.ErrDocumentNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrCollectionExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, store.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		h.log.Error("处理请求失败", "method", method, "err", err)
		return status.Error(codes.Internal, "服务内部错误")
	}
}

// toStoreDocument 将 proto Document 转换为 store.Document
func toStoreDocument(doc *vectorstorev1.Document) store.Document {
	return store.Document{
		ID:        doc.GetId(),
		Embedding: doc.GetEmbedding(),
		Content:   doc.GetContent(),
		Metadata:  doc.GetMetadata(),
	}
}

// toProtoDocument 将 store.Document 转换为 proto Document
func toProtoDocument(doc store.Document) *vectorstorev1.Document {
	return &vectorstorev1.Document{
		Id:        doc.ID,
		Embedding: doc.Embedding,
		Content:   doc.Content,
		Metadata:  doc.Metadata,
	}
}

// toProtoResult 将 store.Result 转换为 proto SearchResult
func toProtoResult(result store.Result) *vectorstorev1.SearchResult {
	return &vectorstorev1.SearchResult{
		Id:         result.ID,
		Content:    result.Content,
		Metadata:   result.Metadata,
		Similarity: result.Similarity,
	}
}
