package store

import (
	"context"
	"fmt"
	"io"
	"log"
	"mime"
	"path"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinioStore struct {
	client *minio.Client
	bucket string //桶名称
	//Endpoint string //服务地址 localhost:9000
	//UseSSL   bool   //是否使用https
}

var _ Store = (*MinioStore)(nil)

func NewMinioStore(ctx context.Context, endpoint, bucket string, useSSL bool, creds *credentials.Credentials) (*MinioStore, error) {
	//1. 创建 MinIO 客户端
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  creds,
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 MinIO client: %w", err)
	}
	// 2. 检查 Bucket 是否存在
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("检查 bucket %q: %w", bucket, err)
	}
	// 3. 不存在就创建
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("创建 bucket %q: %w", bucket, err)
		}
	}
	// 4. 返回 MinioStore
	return &MinioStore{
		client: client,
		bucket: bucket,
	}, nil
}

func (m *MinioStore) Save(ctx context.Context, key string, reader io.Reader) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("操作取消: %w", err)
	}
	//根据文件扩展名判断 MIME 类型
	contentType := mime.TypeByExtension(path.Ext(key))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := m.client.PutObject(ctx, m.bucket, key, reader, -1, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("上传对象 %q: %w", key, err)
	}
	return nil
}

func (m *MinioStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("操作取消: %w", err)
	}
	if key == "" {
		log.Printf("删除跳过:key为空")
		return nil
	}
	//contentType := mime.TypeByExtension(path.Ext(key))
	//contentType 是对象的元数据，只在上传时有意义：告诉 MinIO "这个对象是什么类型"，
	//以后下载或通过 URL 访问时，响应头里的 Content-Type 就来自这里
	return m.client.RemoveObject(ctx, m.bucket, key, minio.RemoveObjectOptions{})
}

func (m *MinioStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("操作取消: %w", err)
	}
	if key == "" {
		return nil, fmt.Errorf("对象 key 不能为空")
	}
	return m.client.GetObject(ctx, m.bucket, key, minio.GetObjectOptions{})
}
