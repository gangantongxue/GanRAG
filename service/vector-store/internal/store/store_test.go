package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/gangantongxue/GanRAG/service/vector-store/internal/config"
)

// newMemoryStore 创建内存模式的 Store，用于测试
func newMemoryStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(config.StorageConfig{Mode: config.StorageModeMemory})
	if err != nil {
		t.Fatalf("创建内存 Store 失败: %v", err)
	}
	return st
}

// vec3 构造三维测试向量
func vec3(x, y, z float32) []float32 {
	return []float32{x, y, z}
}

// TestCollectionLifecycle 测试集合创建、查重、列表与删除
func TestCollectionLifecycle(t *testing.T) {
	st := newMemoryStore(t)

	if err := st.CreateCollection("docs", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("创建集合失败: %v", err)
	}
	if err := st.CreateCollection("docs", nil); !errors.Is(err, ErrCollectionExists) {
		t.Fatalf("重复创建集合应返回 ErrCollectionExists，实际: %v", err)
	}
	if err := st.CreateCollection("", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空名称应返回 ErrInvalidArgument，实际: %v", err)
	}

	infos := st.ListCollections()
	if len(infos) != 1 || infos[0].Name != "docs" || infos[0].Count != 0 {
		t.Fatalf("ListCollections 结果不符合预期: %+v", infos)
	}

	if err := st.DeleteCollection("docs"); err != nil {
		t.Fatalf("删除集合失败: %v", err)
	}
	if err := st.DeleteCollection("docs"); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("删除不存在的集合应返回 ErrCollectionNotFound，实际: %v", err)
	}
}

// TestWriteSearchGetDelete 测试写入、检索、按 ID 查询与删除的完整流程
func TestWriteSearchGetDelete(t *testing.T) {
	st := newMemoryStore(t)
	ctx := t.Context()

	if err := st.CreateCollection("docs", nil); err != nil {
		t.Fatalf("创建集合失败: %v", err)
	}

	docs := []Document{
		{ID: "a", Embedding: vec3(1, 0, 0), Content: "苹果", Metadata: map[string]string{"type": "fruit"}},
		{ID: "b", Embedding: vec3(0, 1, 0), Content: "香蕉", Metadata: map[string]string{"type": "fruit"}},
		{ID: "c", Embedding: vec3(0, 0, 1), Content: "胡萝卜", Metadata: map[string]string{"type": "vegetable"}},
	}
	written, err := st.Write(ctx, "docs", docs, false)
	if err != nil {
		t.Fatalf("写入文档失败: %v", err)
	}
	if written != 3 {
		t.Fatalf("写入数量 = %d，期望 3", written)
	}

	// 查询向量与 a 完全一致，a 应排在第一位
	results, err := st.Search(ctx, "docs", vec3(1, 0, 0), 3, nil, nil)
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(results) != 3 || results[0].ID != "a" {
		t.Fatalf("检索结果不符合预期: %+v", results)
	}

	// topK 超过文档数时应按实际数量返回
	results, err = st.Search(ctx, "docs", vec3(1, 0, 0), 10, nil, nil)
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("topK 超限时结果数 = %d，期望 3", len(results))
	}

	// 元数据过滤
	results, err = st.Search(ctx, "docs", vec3(1, 0, 0), 10, map[string]string{"type": "vegetable"}, nil)
	if err != nil {
		t.Fatalf("带过滤检索失败: %v", err)
	}
	if len(results) != 1 || results[0].ID != "c" {
		t.Fatalf("元数据过滤结果不符合预期: %+v", results)
	}

	// 按 ID 查询
	doc, err := st.GetByID(ctx, "docs", "a")
	if err != nil {
		t.Fatalf("按 ID 查询失败: %v", err)
	}
	if doc.Content != "苹果" || len(doc.Embedding) != 3 {
		t.Fatalf("查询结果不符合预期: %+v", doc)
	}
	if _, err := st.GetByID(ctx, "docs", "missing"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("查询不存在的文档应返回 ErrDocumentNotFound，实际: %v", err)
	}

	// 按 ID 删除
	if err := st.Delete(ctx, "docs", []string{"a"}, nil, nil); err != nil {
		t.Fatalf("按 ID 删除失败: %v", err)
	}
	if _, err := st.GetByID(ctx, "docs", "a"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("删除后查询应返回 ErrDocumentNotFound，实际: %v", err)
	}

	// 无删除条件应报错，避免误删全部文档
	if err := st.Delete(ctx, "docs", nil, nil, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("无删除条件应返回 ErrInvalidArgument，实际: %v", err)
	}

	// 按元数据条件删除
	if err := st.Delete(ctx, "docs", nil, map[string]string{"type": "fruit"}, nil); err != nil {
		t.Fatalf("按元数据删除失败: %v", err)
	}
	infos := st.ListCollections()
	if len(infos) != 1 || infos[0].Count != 1 {
		t.Fatalf("删除后集合信息不符合预期: %+v", infos)
	}
}

// TestWriteUpsert 测试同 ID 重复写入时覆盖旧文档
func TestWriteUpsert(t *testing.T) {
	st := newMemoryStore(t)
	ctx := t.Context()

	if _, err := st.Write(ctx, "docs", []Document{
		{ID: "a", Embedding: vec3(1, 0, 0), Content: "旧内容"},
	}, true); err != nil {
		t.Fatalf("写入文档失败: %v", err)
	}
	if _, err := st.Write(ctx, "docs", []Document{
		{ID: "a", Embedding: vec3(0, 1, 0), Content: "新内容"},
	}, false); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}

	doc, err := st.GetByID(ctx, "docs", "a")
	if err != nil {
		t.Fatalf("按 ID 查询失败: %v", err)
	}
	if doc.Content != "新内容" {
		t.Fatalf("内容 = %q，期望 新内容", doc.Content)
	}
	infos := st.ListCollections()
	if infos[0].Count != 1 {
		t.Fatalf("覆盖写入后文档数 = %d，期望 1", infos[0].Count)
	}
}

// TestWriteValidation 测试写入参数校验
func TestWriteValidation(t *testing.T) {
	st := newMemoryStore(t)
	ctx := t.Context()

	// 空批次
	if _, err := st.Write(ctx, "docs", nil, true); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空批次应返回 ErrInvalidArgument，实际: %v", err)
	}
	// 集合不存在且不允许自动创建
	if _, err := st.Write(ctx, "docs", []Document{{ID: "a", Embedding: vec3(1, 0, 0)}}, false); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("集合不存在应返回 ErrCollectionNotFound，实际: %v", err)
	}
	// create_if_missing=true 时自动创建
	if written, err := st.Write(ctx, "docs", []Document{{ID: "a", Embedding: vec3(1, 0, 0)}}, true); err != nil || written != 1 {
		t.Fatalf("自动创建集合并写入失败: written=%d, err=%v", written, err)
	}
	// 缺少 ID
	if _, err := st.Write(ctx, "docs", []Document{{Embedding: vec3(1, 0, 0)}}, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("缺少 ID 应返回 ErrInvalidArgument，实际: %v", err)
	}
	// 缺少 embedding
	if _, err := st.Write(ctx, "docs", []Document{{ID: "b"}}, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("缺少 embedding 应返回 ErrInvalidArgument，实际: %v", err)
	}
	// 同一批次内维度不一致
	if _, err := st.Write(ctx, "docs", []Document{
		{ID: "b", Embedding: vec3(1, 0, 0)},
		{ID: "c", Embedding: []float32{1, 0}},
	}, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("批次内维度不一致应返回 ErrInvalidArgument，实际: %v", err)
	}
	// 与集合已有维度不一致
	if _, err := st.Write(ctx, "docs", []Document{{ID: "b", Embedding: []float32{1, 0}}}, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("与集合维度不一致应返回 ErrInvalidArgument，实际: %v", err)
	}
}

// TestSearchValidation 测试检索参数校验与空集合行为
func TestSearchValidation(t *testing.T) {
	st := newMemoryStore(t)
	ctx := t.Context()

	if err := st.CreateCollection("empty", nil); err != nil {
		t.Fatalf("创建集合失败: %v", err)
	}
	// 空集合返回空结果
	results, err := st.Search(ctx, "empty", vec3(1, 0, 0), 1, nil, nil)
	if err != nil || len(results) != 0 {
		t.Fatalf("空集合检索应返回空结果，实际: results=%v, err=%v", results, err)
	}
	// top_k 非法
	if _, err := st.Search(ctx, "empty", vec3(1, 0, 0), 0, nil, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("top_k=0 应返回 ErrInvalidArgument，实际: %v", err)
	}
	// 查询向量为空
	if _, err := st.Search(ctx, "empty", nil, 1, nil, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空查询向量应返回 ErrInvalidArgument，实际: %v", err)
	}
	// 集合不存在
	if _, err := st.Search(ctx, "missing", vec3(1, 0, 0), 1, nil, nil); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("集合不存在应返回 ErrCollectionNotFound，实际: %v", err)
	}
}

// TestPersistentReload 测试持久化模式下重启后数据仍可读取
func TestPersistentReload(t *testing.T) {
	ctx := t.Context()
	cfg := config.StorageConfig{
		Mode:     config.StorageModePersistent,
		Path:     filepath.Join(t.TempDir(), "vector-store"),
		Compress: true,
	}

	st, err := New(cfg)
	if err != nil {
		t.Fatalf("创建持久化 Store 失败: %v", err)
	}
	if _, err := st.Write(ctx, "docs", []Document{
		{ID: "a", Embedding: vec3(1, 0, 0), Content: "苹果"},
		{ID: "b", Embedding: vec3(0, 1, 0), Content: "香蕉"},
	}, true); err != nil {
		t.Fatalf("写入文档失败: %v", err)
	}

	// 模拟重启：重新打开同一数据目录
	reloaded, err := New(cfg)
	if err != nil {
		t.Fatalf("重新打开持久化 Store 失败: %v", err)
	}
	infos := reloaded.ListCollections()
	if len(infos) != 1 || infos[0].Count != 2 {
		t.Fatalf("重启后集合信息不符合预期: %+v", infos)
	}

	results, err := reloaded.Search(ctx, "docs", vec3(1, 0, 0), 1, nil, nil)
	if err != nil {
		t.Fatalf("重启后检索失败: %v", err)
	}
	if len(results) != 1 || results[0].ID != "a" {
		t.Fatalf("重启后检索结果不符合预期: %+v", results)
	}
}
