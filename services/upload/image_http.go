package upload

import (
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

type ImageService struct {
	images *ImageStore
}

func NewImageService(images *ImageStore) *ImageService {
	return &ImageService{images: images}
}

func (s *ImageService) UploadImage() gin.HandlerFunc {
	return func(c *gin.Context) {
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "读取文件失败"})
			return
		}
		defer func() {
			_ = file.Close()
		}()
		key, contentType, err := s.images.Upload(c.Request.Context(), file, header)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"key":         key,
			"contentType": contentType,
		})
	}
}

func (s *ImageService) DeleteImage() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimPrefix(c.Param("key"), "/")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "empty key",
			})
			return
		}
		err := s.images.Delete(c.Request.Context(), key)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"message": "删除成功",
		})
	}
}

func (s *ImageService) OpenImage() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimPrefix(c.Param("key"), "/")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "empty key",
			})
			return
		}
		ext := strings.ToLower(path.Ext(key))
		contentType, ok := allowedExt[ext]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file not allowed"})
			return
		}
		file, err := s.images.Open(c.Request.Context(), key)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "文件打开失败"})
			return
		}
		defer func() {
			_ = file.Close()
		}()
		c.Header("Content-Type", contentType)
		c.Header("Content-Disposition", "inline")
		if _, err := io.Copy(c.Writer, file); err != nil {
			// 此时可能已经向客户端写入部分数据，不能再可靠地返回 JSON 错误。
			return
		}
	}
}
