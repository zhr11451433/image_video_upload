package router

import (
	"context"
	"image_upload/commons/store"
	"image_upload/services/upload"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func SetRouter() *gin.Engine {
	//本地存储
	localStorage := store.NewLocalStore()
	//minio存储
	creds := credentials.NewStaticV4(store.AccessKey, store.SecretKey, "")
	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()
	minioStorage, minioerr := store.NewMinioStore(
		ctx,
		"localhost:9000",
		"demo1",
		false,
		creds,
	)
	if minioerr != nil {
		log.Printf("Error creating minio storage: %v\n", minioerr)
	}
	log.Println("Using minio storage")
	r := gin.Default()
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	//图片本地
	localImages := upload.NewImageStore(localStorage)
	localImageSvc := upload.NewImageService(localImages)
	{
		r.POST("/images", localImageSvc.UploadImage())
		r.POST("/images/*key", localImageSvc.DeleteImage())
		r.GET("/images/*key", localImageSvc.OpenImage())
	}
	//视频本地
	localVideos := upload.NewVideoStore(localStorage)
	localVideoSvc := upload.NewVideoService(localVideos)
	{
		r.POST("/videos", localVideoSvc.UploadVideo())
		r.POST("/videos/*key", localVideoSvc.DeleteVideo())
		r.GET("/videos/*key", localVideoSvc.OpenVideo())
	}
	if minioerr != nil {
		return r
	}
	//minio图片
	minioImage := upload.NewImageStore(minioStorage)
	minioImageSvc := upload.NewImageService(minioImage)
	{
		r.POST("/minio/images", minioImageSvc.UploadImage())
		r.GET("/minio/images/*key", minioImageSvc.OpenImage())
		r.POST("/minio/images/*key", minioImageSvc.DeleteImage())
	}
	//minio视频
	minioVideoSvc := upload.NewVideoService(upload.NewVideoStore(minioStorage))
	{
		r.POST("/minio/videos", minioVideoSvc.UploadVideo())
		r.GET("/minio/videos/*key", minioVideoSvc.OpenVideo())
		r.POST("/minio/videos/*key", minioVideoSvc.DeleteVideo())
	}
	return r
}
