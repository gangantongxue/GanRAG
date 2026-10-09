package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	filestorev1 "github.com/gangantongxue/GanRAG/api/gen/file-store/v1"
	"github.com/gangantongxue/GanRAG/service/file-store/internal/config"
	"github.com/gangantongxue/GanRAG/service/file-store/internal/store"
)

// newTestClient 通过 bufconn 启动临时目录存储的服务端并返回客户端
//
// 客户端收发上限调至 16MB，与服务端一致（gRPC 默认接收上限 4MB 不足以下载 10MB 文件）。
func newTestClient(t *testing.T) filestorev1.FileStoreClient {
	t.Helper()

	st, err := store.New(config.StorageConfig{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(maxMessageSize))
	filestorev1.RegisterFileStoreServer(grpcServer, NewHandler(st, log))
	go func() {
		_ = grpcServer.Serve(lis)
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMessageSize),
			grpc.MaxCallSendMsgSize(maxMessageSize),
		),
	)
	if err != nil {
		t.Fatalf("创建客户端连接失败: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
		grpcServer.Stop()
		_ = lis.Close()
	})

	return filestorev1.NewFileStoreClient(conn)
}

// sha256Hex 计算内容的 sha256 十六进制（小写）
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestFileStoreEndToEnd 端到端测试：上传 -> 元数据 -> 下载 -> 覆盖 -> 列表 -> 删除
func TestFileStoreEndToEnd(t *testing.T) {
	client := newTestClient(t)
	ctx := t.Context()
	payload := []byte("对象存储端到端测试内容")

	// 上传
	up, err := client.Upload(ctx, &filestorev1.UploadRequest{
		Bucket:      "docs",
		Key:         "2026/hello.txt",
		Data:        payload,
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	md := up.GetMetadata()
	if md.GetBucket() != "docs" || md.GetKey() != "2026/hello.txt" {
		t.Fatalf("上传元数据定位不符: %+v", md)
	}
	if md.GetSize() != int64(len(payload)) || md.GetSha256() != sha256Hex(payload) {
		t.Fatalf("上传元数据内容不符: %+v", md)
	}
	if md.GetContentType() != "text/plain" || md.GetUploadedAtUnix() <= 0 {
		t.Fatalf("上传元数据类型/时间不符: %+v", md)
	}

	// 查询元数据
	gm, err := client.GetMetadata(ctx, &filestorev1.GetMetadataRequest{Bucket: "docs", Key: "2026/hello.txt"})
	if err != nil {
		t.Fatalf("查询元数据失败: %v", err)
	}
	if gm.GetMetadata().GetSha256() != md.GetSha256() {
		t.Fatalf("元数据查询结果与上传不一致: %+v", gm.GetMetadata())
	}

	// 下载：内容一致，且元数据 sha256 便于校验
	down, err := client.Download(ctx, &filestorev1.DownloadRequest{Bucket: "docs", Key: "2026/hello.txt"})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if !bytes.Equal(down.GetData(), payload) {
		t.Fatalf("下载内容不符: %q", down.GetData())
	}
	if down.GetMetadata().GetSha256() != sha256Hex(down.GetData()) {
		t.Fatalf("下载元数据 sha256 与内容不匹配: %+v", down.GetMetadata())
	}

	// 覆盖上传：同 bucket+key 覆盖旧文件并更新元数据
	replaced := []byte("覆盖后的内容")
	up2, err := client.Upload(ctx, &filestorev1.UploadRequest{
		Bucket: "docs",
		Key:    "2026/hello.txt",
		Data:   replaced,
	})
	if err != nil {
		t.Fatalf("覆盖上传失败: %v", err)
	}
	if up2.GetMetadata().GetSize() != int64(len(replaced)) || up2.GetMetadata().GetSha256() != sha256Hex(replaced) {
		t.Fatalf("覆盖后元数据不符: %+v", up2.GetMetadata())
	}
	// MIME 类型缺省值
	if up2.GetMetadata().GetContentType() != "application/octet-stream" {
		t.Fatalf("缺省 content type = %q", up2.GetMetadata().GetContentType())
	}
	down, err = client.Download(ctx, &filestorev1.DownloadRequest{Bucket: "docs", Key: "2026/hello.txt"})
	if err != nil || !bytes.Equal(down.GetData(), replaced) {
		t.Fatalf("覆盖后下载不符: err=%v content=%q", err, down.GetData())
	}

	// 列表：bucket 不存在返回空列表
	empty, err := client.List(ctx, &filestorev1.ListRequest{Bucket: "no-such-bucket"})
	if err != nil || len(empty.GetFiles()) != 0 {
		t.Fatalf("不存在的 bucket 应返回空列表: err=%v files=%v", err, empty.GetFiles())
	}
	list, err := client.List(ctx, &filestorev1.ListRequest{Bucket: "docs"})
	if err != nil || len(list.GetFiles()) != 1 || list.GetFiles()[0].GetKey() != "2026/hello.txt" {
		t.Fatalf("列表结果不符: err=%v files=%v", err, list.GetFiles())
	}

	// 删除后所有读取接口返回 NotFound
	if _, err := client.Delete(ctx, &filestorev1.DeleteRequest{Bucket: "docs", Key: "2026/hello.txt"}); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := client.Download(ctx, &filestorev1.DownloadRequest{Bucket: "docs", Key: "2026/hello.txt"}); status.Code(err) != codes.NotFound {
		t.Fatalf("删除后下载应返回 NotFound，实际: %v", err)
	}
	if _, err := client.GetMetadata(ctx, &filestorev1.GetMetadataRequest{Bucket: "docs", Key: "2026/hello.txt"}); status.Code(err) != codes.NotFound {
		t.Fatalf("删除后查询应返回 NotFound，实际: %v", err)
	}
	if _, err := client.Delete(ctx, &filestorev1.DeleteRequest{Bucket: "docs", Key: "2026/hello.txt"}); status.Code(err) != codes.NotFound {
		t.Fatalf("重复删除应返回 NotFound，实际: %v", err)
	}
	list, err = client.List(ctx, &filestorev1.ListRequest{Bucket: "docs"})
	if err != nil || len(list.GetFiles()) != 0 {
		t.Fatalf("删除后列表应为空: err=%v files=%v", err, list.GetFiles())
	}
}

// TestErrorMapping 测试错误的 gRPC 状态码映射
func TestErrorMapping(t *testing.T) {
	client := newTestClient(t)
	ctx := t.Context()

	// 目标不存在 -> NotFound
	if _, err := client.Download(ctx, &filestorev1.DownloadRequest{Bucket: "bkt", Key: "missing.txt"}); status.Code(err) != codes.NotFound {
		t.Fatalf("下载不存在的文件应返回 NotFound，实际: %v", err)
	}
	if _, err := client.Delete(ctx, &filestorev1.DeleteRequest{Bucket: "bkt", Key: "missing.txt"}); status.Code(err) != codes.NotFound {
		t.Fatalf("删除不存在的文件应返回 NotFound，实际: %v", err)
	}
	if _, err := client.GetMetadata(ctx, &filestorev1.GetMetadataRequest{Bucket: "bkt", Key: "missing.txt"}); status.Code(err) != codes.NotFound {
		t.Fatalf("查询不存在的文件应返回 NotFound，实际: %v", err)
	}

	// bucket / key 校验失败、data 非法 -> InvalidArgument
	if _, err := client.Upload(ctx, &filestorev1.UploadRequest{Bucket: "bad bucket", Key: "a.txt", Data: []byte("x")}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("非法 bucket 应返回 InvalidArgument，实际: %v", err)
	}
	if _, err := client.Upload(ctx, &filestorev1.UploadRequest{Bucket: "bkt", Key: "../a.txt", Data: []byte("x")}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("路径穿越 key 应返回 InvalidArgument，实际: %v", err)
	}
	if _, err := client.Upload(ctx, &filestorev1.UploadRequest{Bucket: "bkt", Key: "/a.txt", Data: []byte("x")}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("以 / 开头的 key 应返回 InvalidArgument，实际: %v", err)
	}
	if _, err := client.Upload(ctx, &filestorev1.UploadRequest{Bucket: "bkt", Key: "a.txt"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("空 data 应返回 InvalidArgument，实际: %v", err)
	}
	if _, err := client.List(ctx, &filestorev1.ListRequest{Bucket: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("空 bucket 列表应返回 InvalidArgument，实际: %v", err)
	}
}

// TestSizeLimits 测试 10MB 文件大小边界与 16MB 消息接收上限
func TestSizeLimits(t *testing.T) {
	client := newTestClient(t)
	ctx := t.Context()

	// 恰好 10MB：上传与下载均成功（验证 16MB 消息上限可承载 10MB 文件）
	exact := make([]byte, store.MaxFileSize)
	if _, err := client.Upload(ctx, &filestorev1.UploadRequest{Bucket: "bkt", Key: "big.bin", Data: exact}); err != nil {
		t.Fatalf("10MB 文件应上传成功: %v", err)
	}
	down, err := client.Download(ctx, &filestorev1.DownloadRequest{Bucket: "bkt", Key: "big.bin"})
	if err != nil || len(down.GetData()) != store.MaxFileSize {
		t.Fatalf("10MB 文件应下载成功: err=%v size=%d", err, len(down.GetData()))
	}

	// 超过 10MB：业务校验拒绝 -> InvalidArgument
	tooBig := make([]byte, store.MaxFileSize+1)
	if _, err := client.Upload(ctx, &filestorev1.UploadRequest{Bucket: "bkt", Key: "toobig.bin", Data: tooBig}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("超过 10MB 应返回 InvalidArgument，实际: %v", err)
	}

	// 超过服务端 16MB 接收上限：由 gRPC 框架拒绝 -> ResourceExhausted
	huge := make([]byte, maxMessageSize+1)
	if _, err := client.Upload(ctx, &filestorev1.UploadRequest{Bucket: "bkt", Key: "huge.bin", Data: huge}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("超过 16MB 应返回 ResourceExhausted，实际: %v", err)
	}
}
