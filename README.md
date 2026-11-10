# MuxShift

MuxShift 是一个面向多路音视频组合与时间轴控制的开源项目。

仓库目前是工程骨架，已实现 Go Core 的会话配置模型、事件时间轴、从 HLS 播放列表读取轨道与时间信息、有界的直播回看缓冲与把窗口拼成一份可播放的录制，以及两个客户端：一个用 MyGo 原生播放的桌面实验应用，和一个用 hls.js 在浏览器里播放的 Web 客户端。两边都在同一个 Core 会话里登记多个媒体源，由场景决定显示哪路画面、听见哪路声音。尚未提供媒体采集、跨源同步或混音；音频分离只有一个未接入播放的实验脚本。Web 客户端不做对轴，也不做回看。录制文件本身能播放，但在正在运行的会话窗口里放出回看画面还没有成立。

[English](README.en.md)

## 目录

- `core/`：共享 Go Core 的会话配置模型与事件时间轴。
- `core/hls/`：读取 HLS 播放列表、轨道与首个样本时间，不转码、不对外供流。
- `core/dvr/`：把一个直播源的分片存进有界目录，报告真实可回看区间与缺口，并把一段连续区间拼成一份可播放的录制文件。
- `desktop/`：MyGo 桌面实验应用，把多个本地文件或 http(s) 地址登记进一个 Core 会话，按场景选择画面与声音，管理播放器生命周期，并为直播源保留有限历史窗口。
- `web/`：Web 客户端，用 Go 起一个只监听本机的会话服务，页面用 hls.js 播放 HLS，并演示同一个会话里多路源、场景、音量与播放控制。
- `extension/`：浏览器扩展与它的本机 Host，把用户确认过的一个媒体地址交给正在运行的会话。
- `tools/separate/`：实验性的人声/环境声分离脚本，用自带权重跑分块 ONNX 推理；尚未接入播放路径。
- `docs/development/`：工程架构文档。
- `testplace/`：被 Git 忽略的本地实验、草稿和 Agent 工作材料。

各模块当前状态与依赖方向见[架构说明](docs/development/ARCHITECTURE.zh-CN.md)。

## 验证

安装 Go 1.27 或更新版本，在仓库根目录执行：

```sh
go test ./...
```

这条命令运行 `core` 的会话与时间轴测试、`core/hls` 对录制样例的读取测试，以及 `core/dvr` 的缓冲测试；它不验证媒体功能。

桌面应用需要在 Windows 上单独构建与运行，并会打开一个真实窗口：

```sh
cd desktop
go build .
./desktop.exe -media <本地文件或 http(s) 地址> [-media <第二路> ...]
```

界面上的按钮选择显示哪路画面、听见哪路声音，调整各自的音量与静音，并把某一路按 ±0.1 秒对到另一路上；未对过时间的源默认静音，界面上的按钮可以放开或撤回。「Pause all / Play all」按同步组的成员下发，不作用于组外的源。`-dvr-dir`、`-dvr-retention`、`-dvr-max-bytes` 打开回看缓冲，`-replay <文件>` 把一份拼好的录制作为唯一源播放，窗口标题会标明这是录制回放。`-scenario` 可以跑脚本化的检查（见 `desktop/scenarios.go`，例如 `sync` 是手动对轴、`syncmap` 是单源定位测量、`syncfail` 是成员失败上报、`dvr` 是回看窗口）。它的播放结果来自真实窗口的截图、日志与输出设备电平采样，不进入单元测试。回看缓冲、共同区间、缓存回收与录制文件在单独进程中的播放已经验证；把录制放进正在运行的会话窗口显示画面没有实现。

Web 客户端由同一个 Go 模块构建，只绑定回环地址，并要求启动时打印的令牌：

```sh
go run ./web -media-root testplace/media \
  -media /media/hls-vod/index.m3u8 -media /media/hls-sync-a/index.m3u8
```

它打印 `http://127.0.0.1:<端口>/?token=<令牌>`。打开这个地址后，页面通过 `GET /api/session` 读取会话、通过 `POST /api/scene` 切换显示与声音；令牌只在引导地址里出现一次，随后由 HttpOnly cookie 承担，页面会立刻把它从地址栏移除，本机 Host 则用 `X-MuxShift-Token` 头，而导入端点只认另一个只存在于本机文件里的桥接令牌（`X-MuxShift-Bridge`），因此页面即使带着自己的 cookie 也导入不了任何东西。改场景还要求 JSON 请求体与和请求主机一致的 `Origin`，所以别的页面即使带着浏览器已有的 cookie 也驱动不了会话；每个请求还必须指向回环主机，`-addr` 在启动时就要求是回环地址。媒体文件由同一个进程在 `/media/` 下提供，用 `os.OpenRoot` 打开，`..`、符号链接或目录联接都逃不出 `-media-root`；一次场景改动要么整体生效要么整体拒绝，非法音量不会留下半改的状态。未带令牌的请求返回 403，响应也不带 CORS 头。浏览器只允许用户点击之后出声，因此所有源一开始都是静音的。

Web 端只播放一路 HLS 或 MP4 到各自的媒体元素，不参与跨源对轴，也不保留历史窗口；Seek 只落在浏览器已缓冲的范围内。`hls.js` 1.7.4 的 light 构建与 Apache-2.0 许可证放在 `web/vendor/`。

浏览器扩展把用户点击后选中的那一个地址交给本机会话，不复制 Cookie、请求头或密钥：

```sh
go build -o testplace/bin/muxshift-host.exe ./extension/host
```

把 `extension/` 以开发者模式加载进 Edge，按[扩展说明](extension/README.md)登记 `com.muxshift.bridge` 这个本机 Host，再启动 Web 会话。会话会把地址与桥接令牌写进 `%LOCALAPPDATA%\MuxShift\bridge.json`（`-token-file` 可改，权限 0600），Host 用该令牌以 `X-MuxShift-Bridge` 头调用 `POST /api/import`，扩展本身拿不到令牌；导入日志只写地址的 scheme、host 与路径，带签名参数的地址不会把凭据写进日志。扩展只列出页面已经加载的 `http(s)` 地址：`blob:` 地址属于页面自身，会被拒绝并说明原因；跨源地址按目标站点的 CORS 规则播放，不做代理；受保护媒体一律不碰。`activeTab`、`scripting`、`nativeMessaging` 是全部权限，没有主机权限，因此不点击就读不到任何页面。页面每两秒读取一次会话，导入的源无需刷新即可出现。

`tools/separate/` 是一个实验：它用 UVR MDX-Net 的分块推理把一段录音分成「人声」和「非人声」两条音轨，输出两份可播放的 WAV。它**不在播放路径上**，桌面端与 Web 端都还没有调用它；人声分离尚未成为产品功能。

```sh
python tools\separate\separate.py --model testplace\models\Kim_Vocal_2.onnx \
  --input testplace\media\p11\sample-mixed.wav --out-dir testplace\p11-stems
```

模型权重不随仓库发布，也没有确认可再分发的许可，因此脚本只接受显式传入的 `--model` 路径。在这台 RTX 4060 Laptop 上，44.1 kHz 立体声、5.92 秒一块：DirectML 稳态每块约 0.26 秒（约 7.8 倍实时，显存约 6.2 GiB），纯 CPU 每块约 5.75 秒（比实时更慢）。5.92 秒是**一次模型输入块的长度**，不是实测的端到端延迟：实际等到输出还要算上分块重叠、调度、编码、缓冲与播放器启动，这些没有测量。合成样例上人声轨与解说源相关 0.99、非人声轨与环境声源相关 0.83；真实录音（LibriVox 公版朗读 + Wikimedia 公版掌声，20 秒混音）上人声轨与朗读源相关 0.93、非人声轨与掌声源相关 0.94。真实球场转播里的解说还没有验证，两条 WAV 也还只是同一段录音的派生结果，不是两路独立直播源。细节见 [tools/separate/README.md](tools/separate/README.md)。

## 许可证

见 [LICENSE](LICENSE)。
