# Go 图片与视频上传练习

一个用 Go、Gin 和 MinIO 实现的文件上传练习。图片和视频可以保存到本地磁盘或 MinIO，两种存储都支持上传、查看和删除。

## 功能

- 本地文件保存在 `uploads/`，MinIO 对象保存在 `demo1` 桶。
- 上传时校验文件类型和大小。图片上限为 **5 MiB**，视频上限为 **15 MiB**。
- 视频上传会通过 FFmpeg 的 `ffprobe` 检查格式。
- 同一局域网内的设备可以通过 Gin 服务访问文件。

## 技术栈

| 技术 | 用途 |
| --- | --- |
| Go 1.27 | 编写后端服务 |
| Gin | 注册路由和处理 HTTP 请求 |
| MinIO 与 MinIO Go SDK | 存储对象并调用桶和对象接口 |
| FFmpeg `ffprobe` | 检查视频格式 |

## 项目结构

```text
.
├── main.go           # 启动 HTTP 服务
├── router/           # 初始化存储并注册路由
├── commons/store/    # Store 接口、本地存储和 MinIO 实现
├── services/upload/  # 图片和视频的业务逻辑及 HTTP handler
├── uploads/          # 本地上传目录
└── docs/             # 实现说明
```

本地存储的实现见 [本地图片与视频上传](docs/local-upload.md)。

## 环境准备

- 安装 Go 1.27。
- 启动 MinIO，确认 S3 API 可以通过 `localhost:9000` 访问。MinIO 控制台的地址以启动日志为准。
- 测试视频上传前，确认 [视频业务代码](services/upload/video_service.go) 中的 `ffprobePath` 指向本机的 `ffprobe.exe`。该路径目前是固定的 Windows 路径。

## 运行与验证

### 启动服务

1. 确认 [路由配置](router/router.go) 中的 MinIO 地址、桶名和连接凭据与本地服务一致。
2. 在项目根目录执行 `go run .`，Gin 默认监听 `8080`。应用启动时检查 `demo1`，桶不存在时会尝试创建。
3. 打开 `http://localhost:8080/` 检查 Gin 是否启动。如果日志出现 MinIO 初始化错误，本地接口仍可使用，MinIO 路由不会注册。

同一局域网内的其他设备通过 `http://<运行 Go 的电脑的局域网 IP>:8080` 调用接口。如果连接被 Windows 防火墙拦截，检查 TCP `8080` 的入站规则。Go 服务在本机通过 `9000` 连接 MinIO 的 S3 API。

### 接口一览

| 操作 | 本地图片 | MinIO 图片 | 本地视频 | MinIO 视频 |
| --- | --- | --- | --- | --- |
| 上传 | `POST /images` | `POST /minio/images` | `POST /videos` | `POST /minio/videos` |
| 查看 | `GET /images/{key}` | `GET /minio/images/{key}` | `GET /videos/{key}` | `GET /minio/videos/{key}` |
| 删除 | `POST /images/{key}` | `POST /minio/images/{key}` | `POST /videos/{key}` | `POST /minio/videos/{key}` |

上传请求使用 `multipart/form-data`，文件字段名为 `file`。查看和删除时，将 `{key}` 替换为上传接口返回的完整 key，保留其中的 `images/` 或 `videos/` 前缀。

### 验证 MinIO 图片上传

在 PowerShell 中执行以下命令，将文件路径换成你要上传的 JPG。

```powershell
curl.exe -fsS -F "file=@C:\path\to\test.jpg" http://localhost:8080/minio/images
```

1. 从响应中复制 `key`。假设返回的是 `images/abc.jpg`。
2. 打开 MinIO 控制台，进入 `demo1` 桶，在 `images/` 下找到 `abc.jpg`。核对对象大小，或下载后打开，确认上传成功。
3. 在浏览器访问 `http://localhost:8080/minio/images/images/abc.jpg`，确认 Gin 可以读取并显示图片。
4. 将下面的示例 key 换成实际返回值，执行删除。刷新控制台，确认对应对象消失。

```powershell
curl.exe -fsS -X POST http://localhost:8080/minio/images/images/abc.jpg
```

视频的验证步骤相同，使用 `/minio/videos` 上传，在桶的 `videos/` 前缀下核对对象。

## 核心设计

图片和视频业务分别负责文件校验和生成 key，再调用同一个 `Store` 接口。路由通过注入不同的存储实例选择后端，两种存储复用各自的图片、视频业务逻辑和 HTTP handler。

| 方法 | 本地存储 | MinIO 存储 |
| --- | --- | --- |
| `Save` | 创建目录和文件，将上传流写入磁盘 | 使用 `PutObject` 将上传流写入桶 |
| `Open` | 根据 key 打开本地文件 | 使用 `GetObject` 获取对象读取流 |
| `Delete` | 根据 key 删除本地文件 | 使用 `RemoveObject` 删除桶内对象 |

例如，`images/abc.jpg` 在本地对应 `uploads/images/abc.jpg`，在 MinIO 中对应 `demo1` 桶内的 `images/abc.jpg` 对象。
