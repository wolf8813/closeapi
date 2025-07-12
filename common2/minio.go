package common2

import (
	"bytes"
	"context"
	"io"
	"log"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	endPoint          = "u3j1.or12.idrivee2-3.com"
	accessKey         = "MiGdzck6GHsK0td3nXMG"
	secretKey         = "fyuQr7FS5pYdQZH3j4hxyY8hruygq0O4ndCPvIh3"
	defaultBucketName = "closeai"
)

var MinioClient *minio.Client

// 初始化
// InitIdriveClient 初始化 MinIO 客户端，用于连接到 iDrive 存储服务。
// 该函数会创建 MinIO 客户端实例，并验证与服务端的连接是否正常。
// 若初始化或连接验证过程中出现错误，将返回相应的错误信息；若一切正常，返回 nil。
func InitIdriveClient() error {
	var err error
	// 使用指定的端点、访问密钥和秘密密钥创建一个新的 MinIO 客户端实例
	MinioClient, err = minio.New(endPoint, &minio.Options{
		// 设置凭证信息，使用静态的访问密钥和秘密密钥进行身份验证
		Creds: credentials.NewStaticV4(accessKey, secretKey, ""),
		// 启用安全连接，使用 HTTPS 协议
		Secure: true,
	})
	if err != nil {
		log.Printf("New minioClient failed, err: %v", err)
		return err
	}

	// 验证客户端是否能正常连接到 MinIO 服务
	// 创建一个带有 30 秒超时的上下文，避免长时间等待
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	// 确保在函数结束时取消上下文，释放相关资源
	defer cancel()
	// 调用 ListBuckets 方法列出存储桶，以此验证与服务端的连接
	bucketList, err := MinioClient.ListBuckets(ctx)
	if err != nil {
		// 若连接验证失败，记录错误日志并返回错误信息
		log.Printf("Failed to connect to MinIO server: %v", err)
		return err
	}

	if len(bucketList) == 0 {
		log.Printf("Failed to connect to MinIO server, bucketList is empty")
		return err
	}
	//遍历打印所有buketName
	for _, bucket := range bucketList {
		log.Printf("BucketName: %s", bucket.Name)
	}

	// 若客户端创建和连接验证都成功，返回 nil 表示初始化成功
	return nil
}

func UploadToIdrive(ctx context.Context, bucketName string, objectKey string, content []byte) (string, error) {
	if bucketName == "" {
		bucketName = defaultBucketName
	}
	reader := bytes.NewReader(content)
	uploadInfo, err := MinioClient.PutObject(ctx, bucketName, objectKey, reader, int64(len(content)), minio.PutObjectOptions{})
	if err != nil {
		log.Fatalf("UploadToIdrive failed, err: %v", err)
		return "", err
	}
	log.Printf("UploadToIdrive success, uploadInfo: %v", uploadInfo)
	// 返回对象唯一key或者URL
	return objectKey, nil
}

func DownloadFromIdrive(ctx context.Context, bucketName string, objectKey string) ([]byte, error) {
	if bucketName == "" {
		bucketName = defaultBucketName
	}
	obj, err := MinioClient.GetObject(ctx, bucketName, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(obj)
}
