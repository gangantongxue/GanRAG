package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vectorstorev1 "github.com/gangantongxue/GanRAG/api/gen/vector-store/v1"
	"github.com/gangantongxue/GanRAG/service/vector-store/internal/config"
	"github.com/gangantongxue/GanRAG/service/vector-store/internal/store"
)

// newTestClient 通过 bufconn 启动内存版服务端并返回客户端
func newTestClient(t *testing.T) vectorstorev1.VectorStoreClient {
	t.Helper()

	st, err := store.New(config.StorageConfig{Mode: config.StorageModeMemory})
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	vectorstorev1.RegisterVectorStoreServer(grpcServer, NewHandler(st, log))
	go func() {
		_ = grpcServer.Serve(lis)
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("创建客户端连接失败: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
		grpcServer.Stop()
		_ = lis.Close()
	})

	return vectorstorev1.NewVectorStoreClient(conn)
}

// TestVectorStoreEndToEnd 端到端测试：创建集合 -> 写入 -> 检索 -> 查询 -> 删除 -> 列表
func TestVectorStoreEndToEnd(t *testing.T) {
	client := newTestClient(t)
	ctx := t.Context()

	// 创建集合
	if _, err := client.CreateCollection(ctx, &vectorstorev1.CreateCollectionRequest{Name: "docs"}); err != nil {
		t.Fatalf("创建集合失败: %v", err)
	}
	// 重复创建返回 AlreadyExists
	_, err := client.CreateCollection(ctx, &vectorstorev1.CreateCollectionRequest{Name: "docs"})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("重复创建集合应返回 AlreadyExists，实际: %v", err)
	}

	// 写入文档
	writeResp, err := client.Write(ctx, &vectorstorev1.WriteRequest{
		Collection: "docs",
		Documents: []*vectorstorev1.Document{
			{Id: "a", Embedding: []float32{1, 0, 0}, Content: "苹果", Metadata: map[string]string{"type": "fruit"}},
			{Id: "b", Embedding: []float32{0, 1, 0}, Content: "香蕉", Metadata: map[string]string{"type": "fruit"}},
		},
	})
	if err != nil {
		t.Fatalf("写入文档失败: %v", err)
	}
	if writeResp.GetWritten() != 2 {
		t.Fatalf("写入数量 = %d，期望 2", writeResp.GetWritten())
	}

	// 检索：查询向量与 a 一致，a 应排第一
	searchResp, err := client.Search(ctx, &vectorstorev1.SearchRequest{
		Collection:     "docs",
		QueryEmbedding: []float32{1, 0, 0},
		TopK:           5,
	})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(searchResp.GetResults()) != 2 || searchResp.GetResults()[0].GetId() != "a" {
		t.Fatalf("检索结果不符合预期: %+v", searchResp.GetResults())
	}

	// 按 ID 查询
	getResp, err := client.GetByID(ctx, &vectorstorev1.GetByIDRequest{Collection: "docs", Id: "a"})
	if err != nil {
		t.Fatalf("按 ID 查询失败: %v", err)
	}
	if getResp.GetDocument().GetContent() != "苹果" {
		t.Fatalf("查询内容 = %q，期望 苹果", getResp.GetDocument().GetContent())
	}

	// 列表
	listResp, err := client.ListCollections(ctx, &vectorstorev1.ListCollectionsRequest{})
	if err != nil {
		t.Fatalf("列出集合失败: %v", err)
	}
	if len(listResp.GetCollections()) != 1 || listResp.GetCollections()[0].GetCount() != 2 {
		t.Fatalf("集合列表不符合预期: %+v", listResp.GetCollections())
	}

	// 按元数据条件删除
	if _, err := client.Delete(ctx, &vectorstorev1.DeleteRequest{
		Collection: "docs",
		Where:      map[string]string{"type": "fruit"},
	}); err != nil {
		t.Fatalf("删除文档失败: %v", err)
	}
	listResp, err = client.ListCollections(ctx, &vectorstorev1.ListCollectionsRequest{})
	if err != nil {
		t.Fatalf("列出集合失败: %v", err)
	}
	if listResp.GetCollections()[0].GetCount() != 0 {
		t.Fatalf("删除后文档数 = %d，期望 0", listResp.GetCollections()[0].GetCount())
	}

	// 删除集合
	if _, err := client.DeleteCollection(ctx, &vectorstorev1.DeleteCollectionRequest{Name: "docs"}); err != nil {
		t.Fatalf("删除集合失败: %v", err)
	}
	_, err = client.GetByID(ctx, &vectorstorev1.GetByIDRequest{Collection: "docs", Id: "a"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("查询已删除集合应返回 NotFound，实际: %v", err)
	}
}

// TestErrorMapping 测试各类错误的 gRPC 状态码映射
func TestErrorMapping(t *testing.T) {
	client := newTestClient(t)
	ctx := t.Context()

	// 集合不存在
	_, err := client.Search(ctx, &vectorstorev1.SearchRequest{
		Collection:     "missing",
		QueryEmbedding: []float32{1, 0},
		TopK:           1,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("检索不存在的集合应返回 NotFound，实际: %v", err)
	}
	// 删除不存在的集合
	_, err = client.DeleteCollection(ctx, &vectorstorev1.DeleteCollectionRequest{Name: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("删除不存在的集合应返回 NotFound，实际: %v", err)
	}

	// 预备数据
	if _, err := client.CreateCollection(ctx, &vectorstorev1.CreateCollectionRequest{Name: "docs"}); err != nil {
		t.Fatalf("创建集合失败: %v", err)
	}
	if _, err := client.Write(ctx, &vectorstorev1.WriteRequest{
		Collection: "docs",
		Documents:  []*vectorstorev1.Document{{Id: "a", Embedding: []float32{1, 0}}},
	}); err != nil {
		t.Fatalf("写入文档失败: %v", err)
	}

	// top_k 非法
	_, err = client.Search(ctx, &vectorstorev1.SearchRequest{
		Collection:     "docs",
		QueryEmbedding: []float32{1, 0},
		TopK:           0,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("top_k=0 应返回 InvalidArgument，实际: %v", err)
	}
	// 查询向量为空
	_, err = client.Search(ctx, &vectorstorev1.SearchRequest{Collection: "docs", TopK: 1})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("空查询向量应返回 InvalidArgument，实际: %v", err)
	}
	// 查询向量维度不一致
	_, err = client.Search(ctx, &vectorstorev1.SearchRequest{
		Collection:     "docs",
		QueryEmbedding: []float32{1, 0, 0},
		TopK:           1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("维度不一致应返回 InvalidArgument，实际: %v", err)
	}
	// 写入缺少 embedding
	_, err = client.Write(ctx, &vectorstorev1.WriteRequest{
		Collection: "docs",
		Documents:  []*vectorstorev1.Document{{Id: "b"}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("缺少 embedding 应返回 InvalidArgument，实际: %v", err)
	}
	// 写入不存在的集合（create_if_missing=false）
	_, err = client.Write(ctx, &vectorstorev1.WriteRequest{
		Collection: "missing",
		Documents:  []*vectorstorev1.Document{{Id: "a", Embedding: []float32{1, 0}}},
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("写入不存在的集合应返回 NotFound，实际: %v", err)
	}
	// 查询不存在的文档
	_, err = client.GetByID(ctx, &vectorstorev1.GetByIDRequest{Collection: "docs", Id: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("查询不存在的文档应返回 NotFound，实际: %v", err)
	}
	// 无删除条件
	_, err = client.Delete(ctx, &vectorstorev1.DeleteRequest{Collection: "docs"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("无删除条件应返回 InvalidArgument，实际: %v", err)
	}
}
