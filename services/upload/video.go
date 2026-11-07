package upload

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image_upload/commons/store"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

type VideoStore struct {
	Storage store.Store
}

var allowExt = map[string]string{
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".mkv":  "video/x-matroska",
}

const maxVideoSize int64 = 15 << 20

func NewVideoStore(store store.Store) *VideoStore {
	return &VideoStore{Storage: store}
}

func (v *VideoStore) Upload(ctx context.Context, file multipart.File, header *multipart.FileHeader) (string, error) {
	if v.Storage == nil {
		return "", errors.New("storage is nil")
	}
	if file == nil {
		return "", errors.New("file is nil")
	}
	if header == nil {
		return "", errors.New("header is nil")
	}
	if header.Size <= 0 {
		return "", errors.New("empty file")
	}

	if header.Size > maxVideoSize {
		return "", errors.New("video too large")
	}
	ext := strings.ToLower(path.Ext(header.Filename))
	_, ok := allowExt[ext]
	if !ok {
		return "", errors.New("invalid file type")
	}
	key, err := newVideoKey(ext)
	if err != nil {
		return "", err
	}
	if err = ProbeAllowedVideo(ctx, file, ext); err != nil {
		return "", err
	}
	if err = v.Storage.Save(ctx, key, &store.MaxBytesReader{R: file, Left: maxVideoSize}); err != nil {
		return "", fmt.Errorf("failed to save file: %w", err)
	}
	return key, nil
}

func newVideoKey(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate file name: %w", err)
	}
	return "videos/" + hex.EncodeToString(buf) + ext, nil
}

func (v *VideoStore) Delete(ctx context.Context, key string) error {
	if v.Storage == nil {
		return errors.New("storage is nil")
	}
	if key == "" {
		return errors.New("empty key")
	}
	return v.Storage.Delete(ctx, key)
}

func (v *VideoStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if v.Storage == nil {
		return nil, errors.New("storage is nil")
	}
	if key == "" {
		return nil, errors.New("empty key")
	}
	return v.Storage.Open(ctx, key)
}

const probeTimeout = 8 * time.Second

type probeResult struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
	} `json:"format"`
}

// ProbeAllowedVideo 用 ffprobe 判断文件是否是 ext 对应的视频。
// file 必须可 Seek。成功或失败后都会把读取位置恢复到开头。
func ProbeAllowedVideo(ctx context.Context, file io.ReadSeeker, ext string) error {
	if _, ok := allowExt[ext]; !ok {
		return errors.New("invalid file type")
	}
	bin, err := ffprobeBin()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "video-probe-*.bin")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err = io.Copy(tmp, &store.MaxBytesReader{R: file, Left: maxVideoSize}); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	formatName, hasVideo, err := runFFprobe(probeCtx, bin, tmpPath)
	if err != nil {
		return err
	}
	if !hasVideo || !videoFormatMatches(ext, formatName) {
		return errors.New("invalid file type")
	}
	return nil
}

const ffprobePath = `C:\Users\HP\AppData\Local\Microsoft\WinGet\Packages\Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe\ffmpeg-9.0.2-full_build\bin\ffprobe.exe`

func ffprobeBin() (string, error) {
	if _, err := os.Stat(ffprobePath); err != nil {
		return "", errors.New("未找到 ffprobe")
	}
	return ffprobePath, nil
}
func runFFprobe(ctx context.Context, bin, path string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, bin,
		"-v", "error",
		"-probesize", "5000000",
		"-analyzeduration", "5000000",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_type:format=format_name",
		"-of", "json",
		path,
	)
	cmd.Stdin = nil
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedBuffer{buf: &stdout, limit: 64 << 10}
	cmd.Stderr = &limitedBuffer{buf: &stderr, limit: 8 << 10}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", false, fmt.Errorf("ffprobe 超时: %w", ctx.Err())
		}
		return "", false, fmt.Errorf("ffprobe 失败: %w", err)
	}
	var result probeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return "", false, fmt.Errorf("解析 ffprobe 结果失败: %w", err)
	}
	hasVideo := false
	for _, stream := range result.Streams {
		if stream.CodecType == "video" {
			hasVideo = true
			break
		}
	}
	return result.Format.FormatName, hasVideo, nil
}
func videoFormatMatches(ext, formatName string) bool {
	found := make(map[string]struct{})
	for part := range strings.SplitSeq(formatName, ",") {
		found[strings.TrimSpace(part)] = struct{}{}
	}
	switch ext {
	case ".mp4", ".mov":
		_, mp4 := found["mp4"]
		_, mov := found["mov"]
		return mp4 || mov
	case ".webm":
		_, ok := found["webm"]
		return ok
	case ".mkv":
		_, ok := found["matroska"]
		return ok
	case ".avi":
		_, ok := found["avi"]
		return ok
	default:
		return false
	}
}

type limitedBuffer struct {
	buf   *bytes.Buffer
	limit int
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	remain := w.limit - w.buf.Len()
	if remain <= 0 {
		return len(p), nil
	}
	if len(p) > remain {
		p = p[:remain]
	}
	_, err := w.buf.Write(p)
	return len(p), err
}
