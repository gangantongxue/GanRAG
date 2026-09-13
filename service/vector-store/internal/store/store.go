// Package store 封装 chromem-go 向量库，提供集合管理、向量写入与相似度检索能力
//
// 本包对上层（gRPC handler）隐藏 chromem-go 的类型与错误，仅暴露自有的
// Document/Result 类型和哨兵错误，便于 handler 做 gRPC 错误码映射。
// chromem-go 内部已对集合与文档加锁，本包的所有方法可并发调用。
package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/philippgille/chromem-go"

	"github.com/gangantongxue/GanRAG/service/vector-store/internal/config"
)

// 哨兵错误：handler 通过 errors.Is 判断并映射为对应 gRPC 状态码
var (
	// ErrInvalidArgument 入参不合法（映射 codes.InvalidArgument）
	ErrInvalidArgument = errors.New("参数不合法")
	// ErrCollectionNotFound 集合不存在（映射 codes.NotFound）
	ErrCollectionNotFound = errors.New("集合不存在")
	// ErrCollectionExists 集合已存在（映射 codes.AlreadyExists）
	ErrCollectionExists = errors.New("集合已存在")
	// ErrDocumentNotFound 文档不存在（映射 codes.NotFound）
	ErrDocumentNotFound = errors.New("文档不存在")
)

// errEmbeddingNotSupported 表示本服务不接受"由存储端生成向量"
//
// chromem 默认的 embeddingFunc 会调用 OpenAI，本服务要求调用方传入向量，
// 因此统一注入该函数：一旦有文档缺少 embedding，写入会立即报错而不是外呼接口。
var errEmbeddingNotSupported = errors.New("vector-store 不生成嵌入向量，请在写入时提供 embedding")

// embeddingFunc 拒绝生成向量，只接受调用方传入的 embedding
func embeddingFunc(context.Context, string) ([]float32, error) {
	return nil, errEmbeddingNotSupported
}

// Document 向量文档
type Document struct {
	ID        string            // 文档唯一 ID
	Embedding []float32         // 嵌入向量
	Content   string            // 原始文本内容
	Metadata  map[string]string // 元数据
}

// Result 检索结果
type Result struct {
	ID         string            // 文档 ID
	Content    string            // 文档原始内容
	Metadata   map[string]string // 文档元数据
	Similarity float32           // 余弦相似度，越大越相似
}

// CollectionInfo 集合概要信息
type CollectionInfo struct {
	Name  string // 集合名称
	Count int    // 文档数量
}

// Store 向量存储
type Store struct {
	db *chromem.DB

	// dims 记录各集合的向量维度，用于写入/检索前的维度一致性校验
	// 0 表示未知（集合刚创建，或服务刚重启尚未写入）
	dimsMu sync.RWMutex
	dims   map[string]int
}

// New 根据存储配置创建 Store
//
// 参数：
//   - cfg: 存储配置，mode 为 memory 时创建内存库，persistent 时创建持久化库
//     （数据目录不存在会自动创建，存在则自动加载已有集合与文档）
//
// 返回：
//   - *Store: Store 实例
//   - error: 创建失败时返回错误
func New(cfg config.StorageConfig) (*Store, error) {
	s := &Store{dims: make(map[string]int)}
	switch cfg.Mode {
	case config.StorageModeMemory:
		s.db = chromem.NewDB()
	case config.StorageModePersistent:
		db, err := chromem.NewPersistentDB(cfg.Path, cfg.Compress)
		if err != nil {
			return nil, fmt.Errorf("创建持久化向量库失败: %w", err)
		}
		s.db = db
	default:
		return nil, fmt.Errorf("%w: 不支持的存储模式 %q", ErrInvalidArgument, cfg.Mode)
	}
	return s, nil
}

// CreateCollection 创建集合
//
// 参数：
//   - name: 集合名称，不能为空
//   - metadata: 集合元数据，可为 nil
//
// 返回：
//   - error: 集合已存在返回 ErrCollectionExists，名称为空返回 ErrInvalidArgument
func (s *Store) CreateCollection(name string, metadata map[string]string) error {
	if name == "" {
		return fmt.Errorf("%w: 集合名称不能为空", ErrInvalidArgument)
	}
	// chromem 的 CreateCollection 对重名集合会直接覆盖旧集合，这里先查重防止误删数据
	if s.db.GetCollection(name, embeddingFunc) != nil {
		return fmt.Errorf("%w: %s", ErrCollectionExists, name)
	}
	if _, err := s.db.CreateCollection(name, metadata, embeddingFunc); err != nil {
		return fmt.Errorf("创建集合失败: %w", err)
	}
	return nil
}

// DeleteCollection 删除集合及其全部文档
//
// 参数：
//   - name: 集合名称
//
// 返回：
//   - error: 集合不存在返回 ErrCollectionNotFound
func (s *Store) DeleteCollection(name string) error {
	if s.db.GetCollection(name, embeddingFunc) == nil {
		return fmt.Errorf("%w: %s", ErrCollectionNotFound, name)
	}
	if err := s.db.DeleteCollection(name); err != nil {
		return fmt.Errorf("删除集合失败: %w", err)
	}
	s.dimsMu.Lock()
	delete(s.dims, name)
	s.dimsMu.Unlock()
	return nil
}

// ListCollections 列出所有集合及文档数量（按名称升序）
func (s *Store) ListCollections() []CollectionInfo {
	collections := s.db.ListCollections()
	infos := make([]CollectionInfo, 0, len(collections))
	for name, collection := range collections {
		infos = append(infos, CollectionInfo{Name: name, Count: collection.Count()})
	}
	slices.SortFunc(infos, func(a, b CollectionInfo) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return infos
}

// Write 批量写入文档（按 ID upsert：同 ID 覆盖旧文档）
//
// 参数：
//   - ctx: 上下文
//   - collection: 目标集合名称
//   - docs: 待写入文档，不能为空；每个文档必须有非空 ID 和 embedding，
//     且同一批次内所有向量维度一致
//   - createIfMissing: 集合不存在时是否自动创建
//
// 返回：
//   - int: 成功写入的文档数量
//   - error: 校验失败返回 ErrInvalidArgument；集合不存在且不允许自动创建返回 ErrCollectionNotFound
func (s *Store) Write(ctx context.Context, collection string, docs []Document, createIfMissing bool) (int, error) {
	if collection == "" {
		return 0, fmt.Errorf("%w: 集合名称不能为空", ErrInvalidArgument)
	}
	if len(docs) == 0 {
		return 0, fmt.Errorf("%w: 待写入文档不能为空", ErrInvalidArgument)
	}
	for i, doc := range docs {
		if doc.ID == "" {
			return 0, fmt.Errorf("%w: 第 %d 个文档 ID 为空", ErrInvalidArgument, i)
		}
		if len(doc.Embedding) == 0 {
			return 0, fmt.Errorf("%w: 文档 %q 缺少 embedding", ErrInvalidArgument, doc.ID)
		}
	}

	// 先做维度校验，避免维度不一致的文档写入集合后导致后续检索报错
	dim := len(docs[0].Embedding)
	for _, doc := range docs {
		if len(doc.Embedding) != dim {
			return 0, fmt.Errorf("%w: 同一批次内向量维度不一致（%d 与 %d）", ErrInvalidArgument, dim, len(doc.Embedding))
		}
	}
	if known := s.dimension(collection); known != 0 && known != dim {
		return 0, fmt.Errorf("%w: 向量维度 %d 与集合 %q 已有维度 %d 不一致", ErrInvalidArgument, dim, collection, known)
	}

	// 获取目标集合
	collectionRef, err := s.resolveCollection(collection, createIfMissing)
	if err != nil {
		return 0, err
	}

	// 转换为 chromem 文档并写入
	chromemDocs := make([]chromem.Document, 0, len(docs))
	for _, doc := range docs {
		chromemDocs = append(chromemDocs, chromem.Document{
			ID:        doc.ID,
			Embedding: doc.Embedding,
			Content:   doc.Content,
			Metadata:  doc.Metadata,
		})
	}
	if err := collectionRef.AddDocuments(ctx, chromemDocs, runtime.NumCPU()); err != nil {
		return 0, fmt.Errorf("写入文档失败: %w", err)
	}

	s.setDimension(collection, dim)
	return len(docs), nil
}

// Search 相似度检索
//
// 参数：
//   - ctx: 上下文
//   - collection: 目标集合名称
//   - embedding: 查询向量，不能为空，维度需与集合内文档一致
//   - topK: 返回结果数量上限，必须大于 0；超过集合文档数时按实际数量返回
//   - where: 元数据精确匹配过滤，可为 nil
//   - whereDocument: 文档内容过滤（支持 $contains / $not_contains），可为 nil
//
// 返回：
//   - []Result: 按相似度降序排列的检索结果，集合为空时返回空切片
//   - error: 集合不存在返回 ErrCollectionNotFound；参数或维度不一致返回 ErrInvalidArgument
func (s *Store) Search(ctx context.Context, collection string, embedding []float32, topK int, where, whereDocument map[string]string) ([]Result, error) {
	if len(embedding) == 0 {
		return nil, fmt.Errorf("%w: 查询向量不能为空", ErrInvalidArgument)
	}
	if topK <= 0 {
		return nil, fmt.Errorf("%w: top_k 必须大于 0", ErrInvalidArgument)
	}
	collectionRef, err := s.getCollection(collection)
	if err != nil {
		return nil, err
	}
	if known := s.dimension(collection); known != 0 && known != len(embedding) {
		return nil, fmt.Errorf("%w: 查询向量维度 %d 与集合 %q 已有维度 %d 不一致", ErrInvalidArgument, len(embedding), collection, known)
	}

	// chromem 要求 nResults <= 文档数，且集合为空时直接返回空结果
	count := collectionRef.Count()
	if count == 0 {
		return []Result{}, nil
	}
	rawResults, err := collectionRef.QueryEmbedding(ctx, embedding, min(topK, count), emptyToNil(where), emptyToNil(whereDocument))
	if err != nil {
		// 服务重启后集合维度未知时，chromem 会在维度不一致时返回该错误
		if strings.Contains(err.Error(), "vectors must have the same length") {
			return nil, fmt.Errorf("%w: 查询向量维度与集合内文档不一致", ErrInvalidArgument)
		}
		return nil, fmt.Errorf("检索失败: %w", err)
	}

	results := make([]Result, 0, len(rawResults))
	for _, r := range rawResults {
		results = append(results, Result{
			ID:         r.ID,
			Content:    r.Content,
			Metadata:   r.Metadata,
			Similarity: r.Similarity,
		})
	}
	return results, nil
}

// Delete 删除集合中的文档
//
// 参数：
//   - ctx: 上下文
//   - collection: 目标集合名称
//   - ids: 待删除文档 ID 列表；非空时忽略 where/whereDocument（chromem 不支持两者叠加）
//   - where: 元数据精确匹配过滤，可为 nil
//   - whereDocument: 文档内容过滤，可为 nil
//
// 返回：
//   - error: 未提供任何删除条件返回 ErrInvalidArgument；集合不存在返回 ErrCollectionNotFound
func (s *Store) Delete(ctx context.Context, collection string, ids []string, where, whereDocument map[string]string) error {
	collectionRef, err := s.getCollection(collection)
	if err != nil {
		return err
	}

	// chromem 在 where/whereDocument 非 nil 时会忽略 ids，这里明确 ids 优先，
	// 避免调用方同时传入两者时产生非预期行为
	if len(ids) > 0 {
		where, whereDocument = nil, nil
	} else if emptyToNil(where) == nil && emptyToNil(whereDocument) == nil {
		return fmt.Errorf("%w: 需要提供 ids 或 where/where_document 过滤条件", ErrInvalidArgument)
	}

	if err := collectionRef.Delete(ctx, emptyToNil(where), emptyToNil(whereDocument), ids...); err != nil {
		return fmt.Errorf("删除文档失败: %w", err)
	}
	return nil
}

// GetByID 按文档 ID 查询
//
// 参数：
//   - ctx: 上下文
//   - collection: 目标集合名称
//   - id: 文档 ID
//
// 返回：
//   - Document: 查询到的文档副本
//   - error: 集合不存在返回 ErrCollectionNotFound，文档不存在返回 ErrDocumentNotFound
func (s *Store) GetByID(ctx context.Context, collection, id string) (Document, error) {
	collectionRef, err := s.getCollection(collection)
	if err != nil {
		return Document{}, err
	}
	if id == "" {
		return Document{}, fmt.Errorf("%w: 文档 ID 不能为空", ErrInvalidArgument)
	}
	doc, err := collectionRef.GetByID(ctx, id)
	if err != nil {
		return Document{}, fmt.Errorf("%w: %s/%s", ErrDocumentNotFound, collection, id)
	}
	return Document{
		ID:        doc.ID,
		Embedding: doc.Embedding,
		Content:   doc.Content,
		Metadata:  doc.Metadata,
	}, nil
}

// resolveCollection 获取目标集合，createIfMissing 为 true 时不存在则创建
func (s *Store) resolveCollection(name string, createIfMissing bool) (*chromem.Collection, error) {
	if createIfMissing {
		collection, err := s.db.GetOrCreateCollection(name, nil, embeddingFunc)
		if err != nil {
			return nil, fmt.Errorf("获取或创建集合失败: %w", err)
		}
		return collection, nil
	}
	return s.getCollection(name)
}

// getCollection 获取集合，不存在时返回 ErrCollectionNotFound
func (s *Store) getCollection(name string) (*chromem.Collection, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: 集合名称不能为空", ErrInvalidArgument)
	}
	collection := s.db.GetCollection(name, embeddingFunc)
	if collection == nil {
		return nil, fmt.Errorf("%w: %s", ErrCollectionNotFound, name)
	}
	return collection, nil
}

// dimension 读取集合已记录的向量维度，未记录时返回 0
func (s *Store) dimension(collection string) int {
	s.dimsMu.RLock()
	defer s.dimsMu.RUnlock()
	return s.dims[collection]
}

// setDimension 记录集合的向量维度
func (s *Store) setDimension(collection string, dim int) {
	s.dimsMu.Lock()
	s.dims[collection] = dim
	s.dimsMu.Unlock()
}

// emptyToNil 将空 map 转为 nil
//
// chromem 的部分接口通过判断 map 是否为 nil 来区分"按条件过滤"与"按 ID 操作"，
// 空 map 会造成语义歧义（如 Delete 会误判为条件删除），因此统一转为 nil
func emptyToNil(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}
