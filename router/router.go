package router

import (
	"image_upload/commons/store"
	"image_upload/services/upload"

	"github.com/gin-gonic/gin"
)

func SetRouter() *gin.Engine {
	localStorage := store.NewLocalStore()

	r := gin.Default()
	r.Static("./uploads", "./uploads")
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	//图片本地
	localImages := upload.NewImageStore(localStorage)
	localImageSvc := upload.NewService(localImages)

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
	return r
}
