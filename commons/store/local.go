package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type LocalStore struct {
	root string
}

func NewLocalStore() *LocalStore {
	return &LocalStore{root: root}
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// path 把 key 解析成 root 下的绝对路径。key 示例：images/123.jpg
func (l *LocalStore) path(key string) (string, error) {
	if l.root == "" {
		return "", errors.New("本地根目录未设置")
	}
	if key == "" || filepath.IsAbs(key) {
		return "", fmt.Errorf("非法 key: %q", key)
	}
	clean := filepath.Clean(key)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("非法 key: %q", key)
	}
	root, err := filepath.Abs(l.root)
	if err != nil {
		return "", fmt.Errorf("解析根目录失败: %w", err)
	}
	full := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", fmt.Errorf("解析路径失败: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("非法 key: %q", key)
	}
	return full, nil
}

func (l *LocalStore) Save(ctx context.Context, key string, reader io.Reader) (err error) {
	if err = ctx.Err(); err != nil {
		return fmt.Errorf("操作取消: %w", err)
	}
	filePath, err := l.path(key)
	if err != nil {
		return err
	}
	dirPath := filepath.Dir(filePath)
	if err = os.MkdirAll(dirPath, 0o755); err != nil {
		return fmt.Errorf("创建目录 %q 失败: %w", dirPath, err)
	}
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("文件创建失败: %w", err)
	}
	defer func() {
		cerr := file.Close()
		if err != nil {
			_ = os.Remove(filePath)
			return
		}
		if cerr != nil {
			_ = os.Remove(filePath)
			err = fmt.Errorf("文件关闭失败: %w", cerr)
		}
	}()
	if _, err = io.Copy(file, ctxReader{ctx: ctx, r: reader}); err != nil {
		return fmt.Errorf("文件写入失败: %w", err)
	}
	return nil
}

func (l *LocalStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("操作取消: %w", err)
	}
	filePath, err := l.path(key)
	if err != nil {
		return err
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("文件删除失败: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("不能删除目录: %q", key)
	}
	if err = os.Remove(filePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("文件删除失败: %w", err)
	}
	return nil
}

func (l *LocalStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("error opening key %s: %v", key, err)
	}
	if key == "" {
		return nil, os.ErrNotExist
	}
	filePath, err := l.path(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("文件打开失败: %w", err)
	}
	return file, nil
}

// MaxBytesReader 读到上限后若还有数据，返回错误，避免把超限文件写完。
// 带计数器的水龙头：水最多流出指定数量，超过就关掉
type MaxBytesReader struct {
	R    io.Reader
	Left int64 //还允许读取多少字节
}

func (m *MaxBytesReader) Read(p []byte) (int, error) {
	if m.Left < 0 {
		return 0, errors.New("file too big")
	}
	if m.Left == 0 {
		var extra [1]byte
		got, err := m.R.Read(extra[:])
		if got > 0 {
			m.Left = -1
			return 0, errors.New("file too big")
		}
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	if int64(len(p)) > m.Left {
		p = p[:m.Left]
	}
	n, err := m.R.Read(p)
	m.Left -= int64(n)
	return n, err
}
