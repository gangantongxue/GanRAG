// Package store 实现基于本地文件夹的对象存储，提供文件上传、下载、删除与元数据管理能力
//
// 存储布局（root 由 storage.path 指定）：
//
//	root/
//	├── <bucket>/<key>        # 对象内容文件（key 允许 "/" 分层）
//	└── meta/<bucket>.json    # 每 bucket 一个元数据索引文件
//
// 元数据索引先写临时文件再 os.Rename 原子替换，避免进程中断留下损坏索引。
// 本包对上层（gRPC handler）仅暴露自有的 Metadata 类型与哨兵错误，
// 便于 handler 做 gRPC 错误码映射。所有方法均可并发调用。
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gangantongxue/GanRAG/service/file-store/internal/config"
)

// MaxFileSize 单个文件的大小上限（10MB），与接口文档约定一致
const MaxFileSize = 10 << 20

// metaDirName 元数据索引目录名（位于存储根目录下）
const metaDirName = "meta"

// defaultContentType 上传未指定 MIME 类型时的缺省值
const defaultContentType = "application/octet-stream"

// 哨兵错误：handler 通过 errors.Is 判断并映射为对应 gRPC 状态码
var (
	// ErrInvalidArgument 入参不合法（映射 codes.InvalidArgument）
	ErrInvalidArgument = errors.New("参数不合法")
	// ErrNotFound 文件不存在（映射 codes.NotFound）
	ErrNotFound = errors.New("文件不存在")
)

// bucketNameRe bucket 命名规则：仅允许字母数字与 - _
var bucketNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// metaEntry 单个文件的元数据（与 meta/<bucket>.json 内的字段一一对应）
type metaEntry struct {
	Size           int64  `json:"size"`             // 文件字节数
	ContentType    string `json:"content_type"`     // MIME 类型
	SHA256         string `json:"sha256"`           // 内容校验和（十六进制小写）
	UploadedAtUnix int64  `json:"uploaded_at_unix"` // 最后上传时间（Unix 秒）
}

// metaIndex 单个 bucket 的元数据索引文件结构
type metaIndex struct {
	Files map[string]metaEntry `json:"files"` // key -> 元数据
}

// Metadata 文件元数据（对外暴露的只读视图）
type Metadata struct {
	Bucket         string // 所属命名空间
	Key            string // 对象路径（已规范化）
	Size           int64  // 文件字节数
	ContentType    string // MIME 类型
	SHA256         string // 内容校验和（十六进制小写）
	UploadedAtUnix int64  // 最后上传时间（Unix 秒）
}

// Store 基于本地文件夹的对象存储
type Store struct {
	root string // 数据根目录

	// mu 串行化所有写操作并保护 metas，保证文件与元数据索引的一致性
	// （文件最大 10MB，单锁的串行开销可接受，换取实现简单可靠）
	mu sync.RWMutex
	// metas 内存中的元数据索引：bucket -> key -> 元数据，启动时从 meta/*.json 加载
	metas map[string]map[string]metaEntry
}

// New 根据存储配置创建 Store
//
// 创建数据根目录与元数据索引目录，并加载已有的 meta/*.json 索引；
// 索引文件损坏（JSON 解析失败）时返回错误，避免带着不一致的索引提供服务。
//
// 参数：
//   - cfg: 对象存储配置（Path 为数据根目录）
//
// 返回：
//   - *Store: Store 实例
//   - error: 目录创建或索引加载失败时返回错误
func New(cfg config.StorageConfig) (*Store, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("%w: storage.path 不能为空", ErrInvalidArgument)
	}
	metaDir := filepath.Join(cfg.Path, metaDirName)
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建存储目录失败: %w", err)
	}

	s := &Store{
		root:  cfg.Path,
		metas: make(map[string]map[string]metaEntry),
	}
	if err := s.loadIndexes(metaDir); err != nil {
		return nil, err
	}
	return s, nil
}

// loadIndexes 启动时加载 meta 目录下全部索引文件
//
// 参数：
//   - metaDir: 元数据索引目录路径
//
// 返回：
//   - error: 索引文件读取或解析失败时返回错误
func (s *Store) loadIndexes(metaDir string) error {
	entries, err := os.ReadDir(metaDir)
	if err != nil {
		return fmt.Errorf("读取元数据索引目录失败: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(metaDir, e.Name())
		payload, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取元数据索引 %s 失败: %w", e.Name(), err)
		}
		var idx metaIndex
		if err := json.Unmarshal(payload, &idx); err != nil {
			return fmt.Errorf("解析元数据索引 %s 失败: %w", e.Name(), err)
		}
		bucket := strings.TrimSuffix(e.Name(), ".json")
		files := make(map[string]metaEntry, len(idx.Files))
		for k, v := range idx.Files {
			files[k] = v
		}
		s.metas[bucket] = files
	}
	return nil
}

// Upload 上传文件；同 bucket+key 已存在时覆盖旧文件并更新元数据
//
// bucket 不存在时自动创建（隐式创建，无独立 CreateBucket 接口）。
// 文件先原子落盘，再更新内存索引并原子持久化索引；索引持久化失败时回滚内存索引。
//
// 参数：
//   - bucket: 命名空间（必填，仅允许字母数字与 - _）
//   - key: 对象路径（必填，允许 "/" 分层，禁止路径穿越）
//   - data: 文件内容（必填，1 字节 ~ 10MB）
//   - contentType: MIME 类型（可选，空值取 application/octet-stream）
//
// 返回：
//   - *Metadata: 上传后的最新元数据
//   - error: 校验失败返回 ErrInvalidArgument，其他错误为落盘失败
func (s *Store) Upload(bucket, key string, data []byte, contentType string) (*Metadata, error) {
	if err := validateBucket(bucket); err != nil {
		return nil, err
	}
	cleanK, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: 文件内容不能为空", ErrInvalidArgument)
	}
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("%w: 文件超过大小上限 %d 字节", ErrInvalidArgument, MaxFileSize)
	}
	if contentType == "" {
		contentType = defaultContentType
	}
	objPath, err := s.objectPath(bucket, cleanK)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 创建 bucket 目录及 key 的父目录
	if err := os.MkdirAll(filepath.Dir(objPath), 0o755); err != nil {
		return nil, fmt.Errorf("创建 bucket 目录失败: %w", err)
	}
	// 内容文件原子写入，避免中断留下半截文件
	if err := writeFileAtomic(objPath, data); err != nil {
		return nil, err
	}

	sum := sha256.Sum256(data)
	entry := metaEntry{
		Size:           int64(len(data)),
		ContentType:    contentType,
		SHA256:         hex.EncodeToString(sum[:]),
		UploadedAtUnix: time.Now().Unix(),
	}

	files, ok := s.metas[bucket]
	if !ok {
		files = make(map[string]metaEntry)
		s.metas[bucket] = files
	}
	old, hadOld := files[cleanK]
	files[cleanK] = entry
	// 索引持久化失败时回滚内存索引，保持与磁盘一致（内容文件已落盘，重新上传即可修复）
	if err := s.persistLocked(bucket); err != nil {
		if hadOld {
			files[cleanK] = old
		} else {
			delete(files, cleanK)
		}
		return nil, err
	}

	return &Metadata{
		Bucket:         bucket,
		Key:            cleanK,
		Size:           entry.Size,
		ContentType:    entry.ContentType,
		SHA256:         entry.SHA256,
		UploadedAtUnix: entry.UploadedAtUnix,
	}, nil
}

// Download 下载文件内容及其元数据
//
// 参数：
//   - bucket: 命名空间
//   - key: 对象路径
//
// 返回：
//   - []byte: 文件内容
//   - *Metadata: 文件元数据（含 sha256，便于调用方校验）
//   - error: 文件不存在返回 ErrNotFound，校验失败返回 ErrInvalidArgument
func (s *Store) Download(bucket, key string) ([]byte, *Metadata, error) {
	cleanK, md, err := s.lookup(bucket, key)
	if err != nil {
		return nil, nil, err
	}
	objPath, err := s.objectPath(bucket, cleanK)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(objPath)
	if errors.Is(err, fs.ErrNotExist) {
		// 索引存在但内容文件缺失（如人工误删），统一按不存在处理
		return nil, nil, fmt.Errorf("%w: %s/%s", ErrNotFound, bucket, cleanK)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("读取文件失败: %w", err)
	}
	return data, md, nil
}

// Delete 删除文件及其元数据
//
// 参数：
//   - bucket: 命名空间
//   - key: 对象路径
//
// 返回：
//   - error: 文件不存在返回 ErrNotFound，校验失败返回 ErrInvalidArgument
func (s *Store) Delete(bucket, key string) error {
	if err := validateBucket(bucket); err != nil {
		return err
	}
	cleanK, err := cleanKey(key)
	if err != nil {
		return err
	}
	objPath, err := s.objectPath(bucket, cleanK)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	files := s.metas[bucket]
	old, ok := files[cleanK]
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, bucket, cleanK)
	}
	// 先删内容文件；文件已不存在时忽略，保证删除操作可重试
	if err := os.Remove(objPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("删除文件失败: %w", err)
	}
	delete(files, cleanK)
	// 索引持久化失败时回滚内存索引，调用方可重试删除
	if err := s.persistLocked(bucket); err != nil {
		files[cleanK] = old
		return err
	}
	return nil
}

// GetMetadata 查询单个文件的元数据（不读取内容）
//
// 参数：
//   - bucket: 命名空间
//   - key: 对象路径
//
// 返回：
//   - *Metadata: 文件元数据
//   - error: 文件不存在返回 ErrNotFound，校验失败返回 ErrInvalidArgument
func (s *Store) GetMetadata(bucket, key string) (*Metadata, error) {
	_, md, err := s.lookup(bucket, key)
	if err != nil {
		return nil, err
	}
	return md, nil
}

// List 列出 bucket 下所有文件的元数据（按 key 升序）
//
// 参数：
//   - bucket: 命名空间（必填）
//
// 返回：
//   - []Metadata: 文件元数据列表；bucket 不存在时返回空列表
//   - error: 校验失败返回 ErrInvalidArgument
func (s *Store) List(bucket string) ([]Metadata, error) {
	if err := validateBucket(bucket); err != nil {
		return nil, err
	}

	s.mu.RLock()
	files := s.metas[bucket]
	result := make([]Metadata, 0, len(files))
	for k, e := range files {
		result = append(result, Metadata{
			Bucket:         bucket,
			Key:            k,
			Size:           e.Size,
			ContentType:    e.ContentType,
			SHA256:         e.SHA256,
			UploadedAtUnix: e.UploadedAtUnix,
		})
	}
	s.mu.RUnlock()

	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

// lookup 校验入参并从内存索引中读取元数据
//
// 参数：
//   - bucket: 命名空间
//   - key: 对象路径
//
// 返回：
//   - string: 规范化（filepath.Clean）后的 key
//   - *Metadata: 文件元数据
//   - error: 校验失败返回 ErrInvalidArgument，不存在返回 ErrNotFound
func (s *Store) lookup(bucket, key string) (string, *Metadata, error) {
	if err := validateBucket(bucket); err != nil {
		return "", nil, err
	}
	cleanK, err := cleanKey(key)
	if err != nil {
		return "", nil, err
	}

	s.mu.RLock()
	e, ok := s.metas[bucket][cleanK]
	s.mu.RUnlock()
	if !ok {
		return "", nil, fmt.Errorf("%w: %s/%s", ErrNotFound, bucket, cleanK)
	}
	return cleanK, &Metadata{
		Bucket:         bucket,
		Key:            cleanK,
		Size:           e.Size,
		ContentType:    e.ContentType,
		SHA256:         e.SHA256,
		UploadedAtUnix: e.UploadedAtUnix,
	}, nil
}

// persistLocked 将指定 bucket 的内存索引原子写入 meta/<bucket>.json
//
// 调用方必须已持有 s.mu 写锁。
//
// 参数：
//   - bucket: 命名空间
//
// 返回：
//   - error: 序列化或写盘失败时返回错误
func (s *Store) persistLocked(bucket string) error {
	payload, err := json.MarshalIndent(metaIndex{Files: s.metas[bucket]}, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化元数据索引失败: %w", err)
	}
	path := filepath.Join(s.root, metaDirName, bucket+".json")
	return writeFileAtomic(path, payload)
}

// objectPath 计算对象内容文件的绝对路径，并二次校验其仍在 bucket 根内
//
// 参数：
//   - bucket: 命名空间（已通过 validateBucket）
//   - cleanK: 已规范化的 key
//
// 返回：
//   - string: 内容文件路径
//   - error: 路径超出 bucket 根时返回 ErrInvalidArgument
func (s *Store) objectPath(bucket, cleanK string) (string, error) {
	bucketRoot := filepath.Join(s.root, bucket)
	full := filepath.Join(bucketRoot, cleanK)
	rel, err := filepath.Rel(bucketRoot, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: key 超出 bucket 根目录: %q", ErrInvalidArgument, cleanK)
	}
	return full, nil
}

// validateBucket 校验 bucket 命名：必填、仅允许字母数字与 - _
//
// 参数：
//   - bucket: 待校验的命名空间
//
// 返回：
//   - error: 不合法时返回 ErrInvalidArgument，合法返回 nil
func validateBucket(bucket string) error {
	if bucket == "" {
		return fmt.Errorf("%w: bucket 不能为空", ErrInvalidArgument)
	}
	if !bucketNameRe.MatchString(bucket) {
		return fmt.Errorf("%w: bucket 仅允许字母数字与 - _: %q", ErrInvalidArgument, bucket)
	}
	return nil
}

// cleanKey 校验并规范化对象路径
//
// 规则：必填、禁止以 "/" 开头、禁止包含 "\"、禁止 ".." 路径穿越；
// 允许 "/" 分层，filepath.Clean 规范化后（如 a/b/../c -> a/c）由 objectPath 二次校验。
//
// 参数：
//   - key: 待校验的对象路径
//
// 返回：
//   - string: 规范化后的 key
//   - error: 不合法时返回 ErrInvalidArgument
func cleanKey(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("%w: key 不能为空", ErrInvalidArgument)
	}
	if strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("%w: key 不能以 / 开头: %q", ErrInvalidArgument, key)
	}
	if strings.Contains(key, `\`) {
		return "", fmt.Errorf("%w: key 不能包含 \\: %q", ErrInvalidArgument, key)
	}
	cleaned := filepath.Clean(key)
	// Clean 后仍以 .. 开头，说明会逃逸出 bucket 根目录
	if cleaned == ".." || cleaned == "." ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: key 禁止路径穿越: %q", ErrInvalidArgument, key)
	}
	return cleaned, nil
}

// writeFileAtomic 原子写文件：先写同目录临时文件，再 os.Rename 替换目标
//
// 参数：
//   - path: 目标文件路径
//   - data: 文件内容
//
// 返回：
//   - error: 任一步骤失败时返回错误（残留临时文件会被清理）
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	// 失败路径下尽力清理临时文件；成功（重命名）后 tmpName 置空跳过
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	// CreateTemp 创建的文件权限为 0600，放宽为 0644 与普通数据文件一致
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("设置文件权限失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换目标文件失败: %w", err)
	}
	tmpName = ""
	return nil
}
