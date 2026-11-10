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

var _ Store = (*LocalStore)(nil)

func NewLocalStore() *LocalStore {
	return &LocalStore{root: root}
}

// 给普通的 io.Reader 增加 Context 取消检查，让文件复制操作能够感知请求是否已经被取消
// 如果用户上传图片的过程中断开连接，或者请求超时，程序应该尽可能停止继续写入文件
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

// io.reader是个接口，这里ctxReader实现接口里的方法
// 于是继承了这个接口
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
	//绝对路径 /home/images/123.jpg
	//key 应该只能指定 root 目录内部的文件，不能让调用者直接指定服务器上的任意位置。
	if key == "" || filepath.IsAbs(key) {
		return "", fmt.Errorf("非法 key: %q", key)
	}
	//整理路径，把多余的 .、..、重复分隔符等进行规范化
	//filepath.Clean("images/../123.jpg")
	// 123.jpg
	clean := filepath.Clean(key)
	//不同操作系统的路径分隔符不同，不能直接写死 filepath.Separator
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("非法 key: %q", key)
	}
	root, err := filepath.Abs(l.root)
	if err != nil {
		return "", fmt.Errorf("解析根目录失败: %w", err)
	}
	full := filepath.Join(root, clean)
	//计算从第一个路径到第二个路径，需要经过什么相对路径
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", fmt.Errorf("解析路径失败: %w", err)
	}
	//最终路径是否跑到了 root 目录之外
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
		//io.copy复制写入时出现错误，defer在return之前执行
		//所有这时的err不为nil就会删除文件
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
		//尝试在读取一个字节 只需知道有没有超出限制

		//读到了 1 字节：说明文件超过限制，报错。
		//没读到数据，返回 EOF：说明文件刚好结束，符合限制。
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
	//原本想读取 2048 字节，但只允许读取 1024 字节
	if int64(len(p)) > m.Left {
		p = p[:m.Left]
	}
	n, err := m.R.Read(p)
	m.Left -= int64(n)
	return n, err
}
