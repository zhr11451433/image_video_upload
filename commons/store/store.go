package store

import (
	"context"
	"io"
)

const root = "./uploads"
const AccessKey = "minioadmin"
const SecretKey = "minioadmin"

type Store interface {
	Save(ctx context.Context, key string, reader io.Reader) error
	Delete(ctx context.Context, key string) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}
