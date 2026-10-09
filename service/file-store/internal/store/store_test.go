package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gangantongxue/GanRAG/service/file-store/internal/config"
)

// newTestStore 创建指向临时目录的 Store，用于测试
//
// 返回：
//   - *Store: 数据根目录为 t.TempDir() 的 Store 实例
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return newStoreAt(t, t.TempDir())
}

// newStoreAt 在指定数据目录上创建 Store，用于测试重启加载
//
// 参数：
//   - dir: 数据根目录
//
// 返回：
//   - *Store: Store 实例
func newStoreAt(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := New(config.StorageConfig{Path: dir})
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return st
}

// sha256Hex 计算内容的 sha256 十六进制（小写）
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestNew 测试 Store 创建与元数据索引的重启加载
func TestNew(t *testing.T) {
	dir := t.TempDir()
	st := newStoreAt(t, dir)

	data := []byte("restart-load")
	if _, err := st.Upload("bkt", "a.txt", data, "text/plain"); err != nil {
		t.Fatalf("上传失败: %v", err)
	}

	// 索引文件应写入 meta/<bucket>.json（文档约定的元数据存储格式）
	idxPath := filepath.Join(dir, metaDirName, "bkt.json")
	if _, err := os.Stat(idxPath); err != nil {
		t.Fatalf("元数据索引文件不存在: %v", err)
	}

	// 重启加载：重新打开同一数据目录，元数据应从索引文件恢复
	st2 := newStoreAt(t, dir)
	md, err := st2.GetMetadata("bkt", "a.txt")
	if err != nil {
		t.Fatalf("重启后查询元数据失败: %v", err)
	}
	if md.Size != int64(len(data)) || md.SHA256 != sha256Hex(data) {
		t.Fatalf("重启后元数据不符: %+v", md)
	}
	// 重启后下载的内容也应完整
	got, _, err := st2.Download("bkt", "a.txt")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("重启后下载失败: err=%v content=%q", err, got)
	}

	// 损坏的索引文件应导致启动失败，避免带着不一致索引提供服务
	if err := os.WriteFile(filepath.Join(dir, metaDirName, "bad.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("写入损坏索引失败: %v", err)
	}
	if _, err := New(config.StorageConfig{Path: dir}); err == nil {
		t.Fatal("损坏的索引文件应导致 New 失败")
	}
}

// TestUpload 测试文件上传、元数据生成、覆盖语义与 key 规范化
func TestUpload(t *testing.T) {
	st := newTestStore(t)
	data := []byte("hello file-store")

	// 正常上传：返回元数据并落盘到 <root>/<bucket>/<key>
	md, err := st.Upload("attachments", "article-1/img.png", data, "image/png")
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if md.Bucket != "attachments" || md.Key != "article-1/img.png" {
		t.Fatalf("元数据定位不符: %+v", md)
	}
	if md.Size != int64(len(data)) || md.SHA256 != sha256Hex(data) || md.ContentType != "image/png" {
		t.Fatalf("元数据内容不符: %+v", md)
	}
	if md.UploadedAtUnix <= 0 {
		t.Fatalf("上传时间不合法: %d", md.UploadedAtUnix)
	}
	objPath := filepath.Join(st.root, "attachments", "article-1", "img.png")
	if _, err := os.Stat(objPath); err != nil {
		t.Fatalf("内容文件未落盘到约定位置: %v", err)
	}

	// MIME 类型缺省 application/octet-stream
	md2, err := st.Upload("attachments", "plain.bin", data, "")
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if md2.ContentType != "application/octet-stream" {
		t.Fatalf("缺省 content type = %q，期望 application/octet-stream", md2.ContentType)
	}

	// 覆盖上传：同 bucket+key 覆盖旧文件并更新元数据
	replaced := []byte("replaced content")
	md3, err := st.Upload("attachments", "plain.bin", replaced, "text/plain")
	if err != nil {
		t.Fatalf("覆盖上传失败: %v", err)
	}
	if md3.Size != int64(len(replaced)) || md3.SHA256 != sha256Hex(replaced) {
		t.Fatalf("覆盖后元数据不符: %+v", md3)
	}
	got, _, err := st.Download("attachments", "plain.bin")
	if err != nil || !bytes.Equal(got, replaced) {
		t.Fatalf("覆盖后下载内容不符: err=%v content=%q", err, got)
	}

	// key 规范化：sub/../norm.txt -> norm.txt
	md4, err := st.Upload("attachments", "sub/../norm.txt", data, "")
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if md4.Key != "norm.txt" {
		t.Fatalf("规范化后 key = %q，期望 norm.txt", md4.Key)
	}
	if _, err := st.GetMetadata("attachments", "norm.txt"); err != nil {
		t.Fatalf("按规范化 key 查询失败: %v", err)
	}
}

// TestUploadValidation 测试 bucket 与 key 的校验规则（均应返回 ErrInvalidArgument）
func TestUploadValidation(t *testing.T) {
	st := newTestStore(t)
	data := []byte("x")

	tests := []struct {
		name   string
		bucket string
		key    string
		data   []byte
	}{
		{"bucket 为空", "", "a.txt", data},
		{"bucket 含斜杠", "a/b", "a.txt", data},
		{"bucket 含点号", "a.b", "a.txt", data},
		{"bucket 含空格", "a b", "a.txt", data},
		{"key 为空", "bkt", "", data},
		{"key 以 / 开头", "bkt", "/etc/passwd", data},
		{"key 含反斜杠", "bkt", `dir\a.txt`, data},
		{"key 顶层路径穿越", "bkt", "../outside.txt", data},
		{"key 嵌套路径穿越", "bkt", "dir/../../outside.txt", data},
		{"key 为 ..", "bkt", "..", data},
		{"data 为空", "bkt", "a.txt", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := st.Upload(tt.bucket, tt.key, tt.data, "")
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Upload(%q, %q) 应返回 ErrInvalidArgument，实际: %v", tt.bucket, tt.key, err)
			}
		})
	}
}

// TestUploadSizeLimit 测试 10MB 大小边界
func TestUploadSizeLimit(t *testing.T) {
	st := newTestStore(t)

	// 恰好 10MB：允许
	exact := make([]byte, MaxFileSize)
	if _, err := st.Upload("bkt", "big.bin", exact, ""); err != nil {
		t.Fatalf("10MB 文件应允许上传: %v", err)
	}
	// 超出 1 字节：拒绝
	tooBig := make([]byte, MaxFileSize+1)
	if _, err := st.Upload("bkt", "toobig.bin", tooBig, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("超限文件应返回 ErrInvalidArgument，实际: %v", err)
	}
}

// TestDownload 测试文件下载与 NotFound
func TestDownload(t *testing.T) {
	st := newTestStore(t)
	data := []byte("download me")
	if _, err := st.Upload("bkt", "dir/a.txt", data, "text/plain"); err != nil {
		t.Fatalf("上传失败: %v", err)
	}

	got, md, err := st.Download("bkt", "dir/a.txt")
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("下载内容 = %q，期望 %q", got, data)
	}
	if md.SHA256 != sha256Hex(data) || md.ContentType != "text/plain" {
		t.Fatalf("下载元数据不符: %+v", md)
	}

	// 文件不存在
	if _, _, err := st.Download("bkt", "missing.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("下载不存在的文件应返回 ErrNotFound，实际: %v", err)
	}
	// 非法 key
	if _, _, err := st.Download("bkt", "../x"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("非法 key 应返回 ErrInvalidArgument，实际: %v", err)
	}
}

// TestDelete 测试文件删除及其幂等性
func TestDelete(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Upload("bkt", "a.txt", []byte("a"), ""); err != nil {
		t.Fatalf("上传 a 失败: %v", err)
	}
	if _, err := st.Upload("bkt", "b.txt", []byte("b"), ""); err != nil {
		t.Fatalf("上传 b 失败: %v", err)
	}

	// 删除后：内容文件从磁盘移除，元数据查询 NotFound
	if err := st.Delete("bkt", "a.txt"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	objPath := filepath.Join(st.root, "bkt", "a.txt")
	if _, err := os.Stat(objPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("删除后内容文件应不存在: %v", err)
	}
	if _, err := st.GetMetadata("bkt", "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后查询应返回 ErrNotFound，实际: %v", err)
	}
	// 其他文件不受影响
	if _, _, err := st.Download("bkt", "b.txt"); err != nil {
		t.Fatalf("删除 a 后下载 b 失败: %v", err)
	}
	// 重复删除
	if err := st.Delete("bkt", "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应返回 ErrNotFound，实际: %v", err)
	}
}

// TestGetMetadata 测试元数据查询
func TestGetMetadata(t *testing.T) {
	st := newTestStore(t)
	data := []byte("meta")
	up, err := st.Upload("bkt", "a.txt", data, "text/plain")
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}

	md, err := st.GetMetadata("bkt", "a.txt")
	if err != nil {
		t.Fatalf("查询元数据失败: %v", err)
	}
	if *md != *up {
		t.Fatalf("查询结果 = %+v，期望 %+v", md, up)
	}

	if _, err := st.GetMetadata("bkt", "missing.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("查询不存在的文件应返回 ErrNotFound，实际: %v", err)
	}
	if _, err := st.GetMetadata("", "a.txt"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空 bucket 应返回 ErrInvalidArgument，实际: %v", err)
	}
}

// TestList 测试列表能力：排序、bucket 不存在返回空、非法 bucket
func TestList(t *testing.T) {
	st := newTestStore(t)

	// bucket 不存在返回空列表（而非错误）
	files, err := st.List("missing")
	if err != nil || len(files) != 0 {
		t.Fatalf("不存在的 bucket 应返回空列表: err=%v files=%v", err, files)
	}

	for _, k := range []string{"z.txt", "a.txt", "dir/b.txt"} {
		if _, err := st.Upload("bkt", k, []byte(k), ""); err != nil {
			t.Fatalf("上传 %s 失败: %v", k, err)
		}
	}
	files, err = st.List("bkt")
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	want := []string{"a.txt", "dir/b.txt", "z.txt"}
	if len(files) != len(want) {
		t.Fatalf("列表数量 = %d，期望 %d", len(files), len(want))
	}
	for i, f := range files {
		if f.Key != want[i] {
			t.Fatalf("列表第 %d 项 key = %q，期望 %q（按 key 升序）", i, f.Key, want[i])
		}
	}

	if _, err := st.List("bad bucket"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("非法 bucket 应返回 ErrInvalidArgument，实际: %v", err)
	}
}

// TestConcurrentUpload 并发上传不同 key，配合 -race 检测索引锁的正确性
func TestConcurrentUpload(t *testing.T) {
	st := newTestStore(t)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := filepath.Join("dir", string(rune('a'+i%26))+"-"+string(rune('0'+i/26))+".txt")
			_, errs[i] = st.Upload("bkt", key, []byte("concurrent"), "")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发上传第 %d 个失败: %v", i, err)
		}
	}
	files, err := st.List("bkt")
	if err != nil || len(files) != n {
		t.Fatalf("并发上传后列表 = %d 项（err=%v），期望 %d 项", len(files), err, n)
	}
}
