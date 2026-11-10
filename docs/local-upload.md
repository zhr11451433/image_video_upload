# 本地图片与视频上传

这份笔记只讲本地磁盘存储。请求从 Gin 处理器进来，经过图片或视频的业务层，再落到 `store.Store`。本地实现是 `LocalStore`，文件都写在 `./uploads` 下面。

`commons/store/minio.go` 不在这份笔记里。

## 一次请求怎么走完

图片和视频是同一套骨架。差别只在业务层：图片用文件头判断类型，视频用 ffprobe 判断容器和视频流。

```text
客户端
  │
  ▼
Gin 处理器          ImageService / VideoService
  │                 读表单、取路径参数、写 JSON
  ▼
业务层              ImageStore / VideoStore
  │                 校验大小和类型、生成 key
  ▼
存储接口            store.Store
  │                 Save / Delete / Open
  ▼
本地实现            LocalStore
                    把 key 收成安全路径，再读写磁盘
```

上传时业务层会把文件包进 `MaxBytesReader`，再交给 `Save`。这样即使前面的大小检查被绕过，写入过程中超过上限也会停下来。

打开和删除不重新校验文件内容。打开时只根据 key 的扩展名决定 `Content-Type`。

## 存储层

文件：`commons/store/store.go`、`commons/store/local.go`。

### `Store`

```go
type Store interface {
    Save(ctx context.Context, key string, reader io.Reader) error
    Delete(ctx context.Context, key string) error
    Open(ctx context.Context, key string) (io.ReadCloser, error)
}
```

这是存储契约。图片和视频只依赖这三个方法，不关心文件在本地还是以后换到别的存储。

| 方法 | 作用 |
| --- | --- |
| `Save` | 把 `reader` 的内容写到 `key` |
| `Delete` | 删掉 `key` 对应的文件 |
| `Open` | 打开 `key`，返回可读可关闭的内容 |

`key` 是相对路径，例如 `images/abc.jpg`、`videos/abc.mp4`。它不是客户端传来的原始文件名。

### `root`

```go
const root = "./uploads"
```

本地文件的根目录。`NewLocalStore` 把它放进 `LocalStore.root`。

### `LocalStore` 和 `NewLocalStore`

```go
type LocalStore struct {
    root string
}
```

本地磁盘上的 `Store`。`root` 是所有 key 的共同前缀。

`NewLocalStore()` 返回根目录为 `./uploads` 的实例。路由启动时调用一次，图片和视频共用这一份。

### `path`

```go
func (l *LocalStore) path(key string) (string, error)
```

把 key 解析成根目录下的绝对路径。`Save`、`Delete`、`Open` 都先走它。

它拒绝这些情况：

- 根目录是空字符串
- key 为空，或 key 本身是绝对路径
- 清理后的 key 是 `.`、`..`，或以 `../` 开头
- 拼出来的路径逃出了根目录

合法示例：`images/123.jpg` 会变成 `<工作目录>/uploads/images/123.jpg`。

### `ctxReader`

```go
type ctxReader struct {
    ctx context.Context
    r   io.Reader
}
```

包了一层 `io.Reader`。每次 `Read` 之前先看 `ctx.Err()`。请求取消或超时后，复制立刻停，不再继续从上传流里读。

只有 `LocalStore.Save` 使用它，包在真正写文件的那个 `io.Copy` 上。

### `Save`

```go
func (l *LocalStore) Save(ctx context.Context, key string, reader io.Reader) error
```

把 `reader` 写到 `path(key)` 指向的文件。

顺序是：

1. 请求已经取消就直接返回。
2. 用 `path` 得到目标路径。
3. `MkdirAll` 创建父目录，权限 `0755`。
4. 用 `O_CREATE|O_EXCL|O_WRONLY` 创建文件，权限 `0644`。`O_EXCL` 保证同名文件已存在时创建失败，不会覆盖旧文件。
5. `io.Copy` 写入。源是 `ctxReader`。
6. 关闭文件。

写入失败、关闭失败时，都会删掉这个半成品文件，避免留下不完整内容。函数用命名返回值 `err`，关闭时的错误可以写回给调用方。

### `Delete`

```go
func (l *LocalStore) Delete(ctx context.Context, key string) error
```

删除一个文件。

- 文件本来就不存在，返回 `nil`。再删一次也是成功。
- 路径是目录，返回错误，不会删目录。
- 先 `Lstat`，不跟随符号链接去判断类型。

### `Open`

```go
func (l *LocalStore) Open(ctx context.Context, key string) (io.ReadCloser, error)
```

打开文件并返回 `*os.File`。调用方读完必须 `Close`。

key 为空时直接返回 `os.ErrNotExist`，不再进入 `path`。文件打不开时错误信息是「文件打开失败」。

### `MaxBytesReader`

```go
type MaxBytesReader struct {
    R    io.Reader
    Left int64
}
```

带上限的读取器。`Left` 是还允许读出的字节数。

- `Left > 0`：本次最多读 `Left` 个字节，读完后扣减。
- `Left == 0`：再试着读 1 个字节。读到了就返回 `file too big`，并把 `Left` 设为 `-1`。一个字节都没有，就返回 `EOF`，表示刚好用完额度。
- `Left < 0`：已经超限，之后每次 `Read` 都返回 `file too big`。

图片上限 5MB，视频上限 15MB。业务层把它套在待保存的文件上，再传给 `Save`。视频探测复制临时文件时也用它。

## 图片

文件：`../services/upload/image_service.go`、`../services/upload/image_http.go`。

### `allowedExt` 和 `maxImageSize`

允许的扩展名和对应的 MIME：

| 扩展名 | Content-Type |
| --- | --- |
| `.jpg`、`.jpeg` | `image/jpeg` |
| `.png` | `image/png` |
| `.gif` | `image/gif` |
| `.webp` | `image/webp` |

`maxImageSize` 是 `5 << 20`，也就是 5MB。上传校验和 `MaxBytesReader` 用同一个数。

### `ImageStore` 和 `NewImageStore`

```go
type ImageStore struct {
    Storage store.Store
}
```

图片业务对象。它负责校验和生成 key，真正读写交给 `Storage`。

`NewImageStore(store)` 把一份 `Store` 放进去。本地路由传入的是 `*LocalStore`。

### `Upload`

```go
func (s *ImageStore) Upload(ctx context.Context, file multipart.File, header *multipart.FileHeader) (string, string, error)
```

保存一张图片。成功时返回 key 和检测到的 Content-Type。

检查顺序：

1. `Storage`、`file`、`header` 都不能是 nil。
2. `header.Size` 必须大于 0，且不超过 5MB。
3. 扩展名必须在 `allowedExt` 里。扩展名来自原始文件名，转成小写后再查。
4. 读最多 512 字节，用 `http.DetectContentType` 看真实类型。读到的类型必须和扩展名对应的 MIME 一致。
5. `Seek(0)` 回到文件开头，否则后面会丢掉已经读过的文件头。
6. `newImageKey` 生成 key。
7. `Storage.Save`，读取器是上限 5MB 的 `MaxBytesReader`。

`header.Size` 来自客户端声明的大小，第 7 步才按实际读到的字节再卡一次上限。

### `newImageKey`

```go
func newImageKey(ext string) (string, error)
```

生成 `images/` 加 16 字节随机数的十六进制，再加扩展名。例如 `images/4f3a....jpg`。随机数来自 `crypto/rand`，不使用用户原来的文件名。

### `Delete` 和 `Open`

```go
func (s *ImageStore) Delete(ctx context.Context, key string) error
func (s *ImageStore) Open(ctx context.Context, key string) (io.ReadCloser, error)
```

两者都先确认 `Storage` 不是 nil、key 不是空字符串，然后调用 `Storage.Delete` 或 `Storage.Open`。这里不再检查扩展名和文件头。扩展名检查在 HTTP 打开接口里做。

### `ImageService` 和 `NewImageService`

```go
type ImageService struct {
    images *ImageStore
}
```

图片的 HTTP 层。三个方法都返回 `gin.HandlerFunc`，路由注册时调用它们拿到处理函数。

`NewImageService(images)` 保存这份 `ImageStore`。

### `UploadImage`

`POST /images`

从表单字段 `file` 取出文件和文件头，调用 `ImageStore.Upload`。处理函数结束前关闭上传文件。

成功 `200`：

```json
{
  "key": "images/<32位十六进制>.jpg",
  "contentType": "image/jpeg"
}
```

读表单失败或业务层返回错误，都是 `400`，正文是 `{"error": "..."}`。

### `DeleteImage`

`POST /images/*key`

Gin 的 `*key` 带前导斜杠，这里用 `TrimPrefix` 去掉。去掉后为空就返回 `400`。

成功 `200`：`{"message": "删除成功"}`。存储层出错也是 `400`。

### `OpenImage`

`GET /images/*key`

同样先去掉前导斜杠。再用扩展名查 `allowedExt`。扩展名不在表里直接 `400`，不会去打开文件。

打开成功后设置：

- `Content-Type`：扩展名对应的 MIME
- `Content-Disposition: inline`

然后把文件内容写到响应体。写到一半失败时不再返回 JSON，因为响应头和部分正文可能已经发出去了。

## 视频

文件：`../services/upload/video_service.go`、`../services/upload/video_http.go`。

骨架和图片相同：校验、生成 key、`Save` / `Delete` / `Open`。视频在保存前多一次 ffprobe。

### `allowExt` 和 `maxVideoSize`

| 扩展名 | Content-Type |
| --- | --- |
| `.mp4` | `video/mp4` |
| `.webm` | `video/webm` |
| `.mov` | `video/quicktime` |
| `.avi` | `video/x-msvideo` |
| `.mkv` | `video/x-matroska` |

`maxVideoSize` 是 `15 << 20`，15MB。

### `VideoStore` 和 `NewVideoStore`

```go
type VideoStore struct {
    Storage store.Store
}
```

视频业务对象，职责和 `ImageStore` 相同。`NewVideoStore(store)` 注入 `Store`。

### `Upload`

```go
func (v *VideoStore) Upload(ctx context.Context, file multipart.File, header *multipart.FileHeader) (string, error)
```

成功时只返回 key。

顺序：

1. 检查 `Storage`、文件、文件头。
2. 大小必须在 0 和 15MB 之间。
3. 扩展名必须在 `allowExt` 里。
4. `newVideoKey` 生成 key。
5. `ProbeAllowedVideo`。探测失败就不保存。
6. `Storage.Save`，读取器上限 15MB。

探测函数会把文件读指针拨回开头，所以第 6 步读到的是完整内容。

### `newVideoKey`

和 `newImageKey` 一样用 16 字节随机数。前缀是 `videos/`，例如 `videos/9c1e....mp4`。

### `Delete` 和 `Open`

与图片的同名方法一样：检查存储和 key，然后转给 `Store`。

### `probeTimeout` 和 `probeResult`

`probeTimeout` 是 8 秒。探测用的 context 从请求 context 派生，到点就取消 ffprobe。

```go
type probeResult struct {
    Streams []struct {
        CodecType string `json:"codec_type"`
    } `json:"streams"`
    Format struct {
        FormatName string `json:"format_name"`
    } `json:"format"`
}
```

只接住 ffprobe JSON 里需要的两块：流的 `codec_type`，以及容器的 `format_name`。

### `ProbeAllowedVideo`

```go
func ProbeAllowedVideo(ctx context.Context, file io.ReadSeeker, ext string) error
```

判断这份可读可定位的内容是不是 `ext` 对应的视频。成功或失败后都会 `Seek` 回开头。

步骤：

1. 扩展名必须在 `allowExt` 里。
2. `ffprobeBin` 确认本机探测程序存在。
3. 创建临时文件 `video-probe-*.bin`，函数结束时关闭并删除。
4. 把上传内容复制进临时文件，复制时套 15MB 的 `MaxBytesReader`。
5. 把原文件拨回开头。
6. 最多等 8 秒，调用 `runFFprobe`。
7. 必须存在视频流，并且 `videoFormatMatches` 认为容器和扩展名相符。

ffprobe 要的是磁盘路径，所以先落临时文件，而不是把上传流直接接进命令的标准输入。

### `ffprobeBin`

返回写死的本机 `ffprobe.exe` 路径。文件不存在时返回「未找到 ffprobe」。这是当前机器上的安装位置，换机器要改这个常量。

### `runFFprobe`

```go
func runFFprobe(ctx context.Context, bin, path string) (string, bool, error)
```

执行 ffprobe，返回容器名，以及是否至少有一条 `codec_type == "video"` 的流。

参数的含义：

- `-v error`：只留错误日志
- `-probesize`、`-analyzeduration`：探测时最多看的数据量和时长，这里都是 5000000
- `-select_streams v:0`：只看第一条视频流
- `-show_entries stream=codec_type:format=format_name`：只要流类型和容器名
- `-of json`：JSON 输出

标准输出最多保留 64KB，标准错误最多保留 8KB，超出的部分由 `limitedBuffer` 丢掉。命令的 `WaitDelay` 是 2 秒：context 取消后，最多再等这么久让进程退出。

context 已取消时，错误写成「ffprobe 超时」。JSON 解析失败时返回「解析 ffprobe 结果失败」。

### `videoFormatMatches`

```go
func videoFormatMatches(ext, formatName string) bool
```

`format_name` 可能是逗号分隔的多个名字，例如 `mov,mp4,m4a,3gp,3g2,mj2`。函数先拆开、去掉空白，再按扩展名判断：

| 扩展名 | 容器名里需要有 |
| --- | --- |
| `.mp4`、`.mov` | `mp4` 或 `mov` |
| `.webm` | `webm` |
| `.mkv` | `matroska` |
| `.avi` | `avi` |

两边对不上就当成非法类型。`.mp4` 和 `.mov` 共用这一条，是因为 ffprobe 经常给这两种文件同一组容器名。

### `limitedBuffer`

```go
type limitedBuffer struct {
    buf   *bytes.Buffer
    limit int
}
```

给 `bytes.Buffer` 加写入上限。已经达到 `limit` 后，后面的写入直接丢弃，但对调用方仍返回「全部写成功」。这样 ffprobe 输出异常变长时，内存停在上限，命令本身也不会因为管道写满而卡住。

### `VideoService` 和 `NewVideoService`

```go
type VideoService struct {
    videos *VideoStore
}
```

视频的 HTTP 层，对应图片的 `ImageService`。

### `UploadVideo`

`POST /videos`

表单字段同样是 `file`。成功 `200` 只有 key：

```json
{
  "key": "videos/<32位十六进制>.mp4"
}
```

没有图片响应里的 `contentType`。失败是 `400`。

### `DeleteVideo`

`POST /videos/*key`

处理和图片删除相同。成功正文是 `{"message": "删除成功"}`。

### `OpenVideo`

`GET /videos/*key`

用 `allowExt` 把扩展名换成视频 MIME，设置 `Content-Type` 和 `Content-Disposition: inline`，再把文件写到响应。写到一半失败时同样不再补 JSON。

## 怎么接上

### `SetRouter`

文件：`router/router.go`。

```go
func SetRouter() *gin.Engine
```

启动时组好依赖，并注册路由。

1. `store.NewLocalStore()` 得到一份本地存储。
2. `gin.Default()` 创建引擎。
3. `r.Static("./uploads", "./uploads")` 把磁盘目录映射成静态资源。直接访问这个目录的 URL 也能拿到文件，和下面的打开接口是两条路。
4. `GET /` 返回 `{"status":"ok"}`。
5. 图片：`NewImageStore(localStorage)`，再 `NewImageService`，注册三条路由。
6. 视频：`NewVideoStore(localStorage)`，再 `NewVideoService`，注册三条路由。

图片和视频共用同一个 `LocalStore`，文件靠 key 前缀 `images/`、`videos/` 分开。

### `main`

文件：`main.go`。

监听 `:8080`。另外监听 `SIGINT` 和 `SIGTERM`。

- `ListenAndServe` 出现 `http.ErrServerClosed` 以外的错误：记日志并退出进程。
- 收到中断信号：最多再等 5 秒做 `Shutdown`，让已经进来的请求结束。
- 这 5 秒内关不干净：再 `Close` 强制断开。

## 接口怎么调

服务地址按 `http://127.0.0.1:8080`。上传的表单字段名是 `file`。

删除和打开的路径参数是上传返回的完整 key。key 自己已经带 `images/` 或 `videos/`，路由前缀又是 `/images/`、`/videos/`，所以 URL 里这个目录名会出现两次。

### 图片

```bash
curl -F "file=@cat.jpg" http://127.0.0.1:8080/images
```

```bash
curl -X POST http://127.0.0.1:8080/images/images/<文件名>.jpg
```

```bash
curl -o out.jpg http://127.0.0.1:8080/images/images/<文件名>.jpg
```

### 视频

```bash
curl -F "file=@clip.mp4" http://127.0.0.1:8080/videos
```

```bash
curl -X POST http://127.0.0.1:8080/videos/videos/<文件名>.mp4
```

```bash
curl -o out.mp4 http://127.0.0.1:8080/videos/videos/<文件名>.mp4
```

### 常见失败

这些情况都是 HTTP `400`，正文为 `{"error":"..."}`：

| 场景 | `error` 大致内容 |
| --- | --- |
| 没有表单字段 `file` | `读取文件失败` |
| 空文件 | `empty file` |
| 图片超过 5MB，或视频超过 15MB | `file too big` 或 `video too large` |
| 扩展名不在允许表里 | `file not allowed` 或 `invalid file type` |
| 图片扩展名和文件头不一致 | `file content does not match extension` |
| 视频容器和扩展名对不上，或没有视频流 | `invalid file type` |
| 本机没有那份 ffprobe | `未找到 ffprobe` |
| 删除或打开时 key 为空 | `empty key` |
| 打开一个不存在的文件 | `文件打开失败` |
