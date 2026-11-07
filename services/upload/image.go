package upload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image_upload/commons/store"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
)

var allowedExt = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
}

const maxImageSize int64 = 5 << 20

type ImageStore struct {
	Storage store.Store
}

func NewImageStore(store store.Store) *ImageStore {
	return &ImageStore{Storage: store}
}

func (s *ImageStore) Upload(ctx context.Context, file multipart.File, header *multipart.FileHeader) (string, string, error) {
	if s.Storage == nil {
		return "", "", errors.New("storage is nil")
	}
	if file == nil {
		return "", "", errors.New("file is nil")
	}
	if header == nil {
		return "", "", errors.New("file header is nil")
	}
	if header.Size <= 0 {
		return "", "", errors.New("empty file")
	}
	if header.Size > maxImageSize {
		return "", "", errors.New("file too big")
	}
	// 原始文件名，例如 cat.jpg
	ext := strings.ToLower(path.Ext(header.Filename))
	expectedType, ok := allowedExt[ext]
	if !ok {
		return "", "", errors.New("file not allowed")
	}
	headerBytes := make([]byte, 512)
	n, err := io.ReadFull(file, headerBytes)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", err
	}
	if n == 0 {
		return "", "", errors.New("empty file")
	}
	contentType := http.DetectContentType(headerBytes[:n])
	if contentType != expectedType {
		return "", "", errors.New("file content does not match extension")
	}
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return "", "", err
	}

	key, err := newImageKey(ext)
	if err != nil {
		return "", "", err
	}
	if err = s.Storage.Save(ctx, key, &store.MaxBytesReader{R: file, Left: maxImageSize}); err != nil {
		return "", "", fmt.Errorf("failed to save file: %w", err)
	}
	return key, contentType, nil
}

func newImageKey(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate file name: %w", err)
	}
	return "images/" + hex.EncodeToString(buf) + ext, nil
}

func (s *ImageStore) Delete(ctx context.Context, key string) error {
	if s.Storage == nil {
		return errors.New("storage is nil")
	}
	if key == "" {
		return errors.New("empty key")
	}
	return s.Storage.Delete(ctx, key)
}

func (s *ImageStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if s.Storage == nil {
		return nil, errors.New("storage is nil")
	}
	if key == "" {
		return nil, errors.New("empty key")
	}
	return s.Storage.Open(ctx, key)
}
