// Package objectstore 封装 Garage（S3 协议）的对象读写。
//
// Repository 是 Garage 的唯一直接访问方，本包只暴露业务需要的最小操作：
// PutObject（整段上传）与 DeleteObject（尽力清理）；对象 key 统一 o/<obj_xid>。
package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	svcconfig "github.com/gangantongxue/GanRAG/service/repository/internal/config"
)

// ErrObjectNotFound 对象不存在（Head 查询）
var ErrObjectNotFound = errors.New("对象不存在")

// ObjectInfo 对象元信息（Head 结果，不读内容）
type ObjectInfo struct {
	Size        int64
	ContentType string
	Metadata    map[string]string // S3 用户元数据（sha256、original-name-b64 等）
}

// Store Garage S3 客户端封装
type Store struct {
	client  *s3.Client
	bucket  string
	timeout time.Duration
}

// New 创建 S3 客户端并探活桶（失败拒绝启动，避免带错误配置提供服务）
func New(cfg svcconfig.ObjectStoreConfig) (*Store, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 S3 配置失败: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = cfg.ForcePathStyle
	})

	st := &Store{client: client, bucket: cfg.Bucket, timeout: cfg.Timeout}
	if err := st.headBucket(context.Background()); err != nil {
		return nil, err
	}
	return st, nil
}

// headBucket 探活桶与凭证
func (s *Store) headBucket(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); err != nil {
		return fmt.Errorf("Garage 桶 %q 不可用: %w", s.bucket, err)
	}
	return nil
}

// Put 写入对象（整段），metadata 为 S3 用户元数据
func (s *Store) Put(ctx context.Context, key string, data []byte, contentType string, metadata map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
		ContentType:   aws.String(contentType),
		Metadata:      metadata,
	})
	if err != nil {
		return fmt.Errorf("写入对象 %s 失败: %w", key, err)
	}
	return nil
}

// Head 查询对象元信息（size/content-type/用户元数据）；对象不存在返回 ErrObjectNotFound
func (s *Store) Head(ctx context.Context, key string) (ObjectInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// 仅 404 归为「对象不存在」，其余错误（网络/凭证）上抛由调用方归一化
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey") {
			return ObjectInfo{}, fmt.Errorf("查询对象 %s 失败: %w", key, ErrObjectNotFound)
		}
		return ObjectInfo{}, fmt.Errorf("查询对象 %s 失败: %w", key, err)
	}

	info := ObjectInfo{Metadata: out.Metadata}
	if out.ContentLength != nil {
		info.Size = *out.ContentLength
	}
	if out.ContentType != nil {
		info.ContentType = *out.ContentType
	}
	return info, nil
}

// Delete 删除对象；S3 DeleteObject 对不存在的 key 也返回成功，天然幂等
func (s *Store) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("删除对象 %s 失败: %w", key, err)
	}
	return nil
}
