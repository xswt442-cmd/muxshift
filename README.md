<div align="center">
  <img src="web/icon.svg" alt="MuxShift" width="88" height="88">
  <h1>MuxShift</h1>
  <p>多路音视频组合与时间轴控制</p>
  <p>
    <a href="README.en.md">English</a> ·
    <a href="docs/development/ARCHITECTURE.zh-CN.md">架构</a> ·
    <a href="desktop/scenarios.go">场景脚本</a>
  </p>
  <p>
    <a href="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml/badge.svg?branch=dev"></a>
    <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-5d77f5"></a>
    <a href="go.mod"><img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-5d77f5"></a>
    <img alt="early" src="https://img.shields.io/badge/status-early%20development-657483">
  </p>
</div>

把多路媒体源放进同一个会话，由场景决定看哪一路画面、听哪几路声音，并在事件时间轴上对齐它们。

目前处于早期阶段：能接入、能切换、能对轴、能回看一小段；采集、混音与跨源自动同步还没做。

## 功能

- **一个 Core 会话**：源、轨道、同步组、场景、输出，加一条事件时间轴——共享事件时间加上每条轨道自己的偏移，就是这条轨道该播的媒体时间。
- **接入 HLS**：读播放列表、探测轨道与首个样本时间。有 PDT 才能跨流对齐，没有就明确拒绝，不猜。
- **桌面端**：MyGo 原生窗口播放多路源，场景切画面与声音，每路独立音量；手动对轴 ±0.1 秒，自动对轴只给建议、不写时间轴。
- **回看缓冲**：直播分片存进有界目录，报告真实可回看区间与缺口，把一段连续区间切成录像；切换是先把新播放器播出画面、再换源，失败就回滚。
- **Web 端**：一个只绑定回环地址的 Go 进程提供服务，页面用 hls.js 播放，能切场景、看事件日志。
- **浏览器扩展**：把用户点选的那一个媒体地址交给本机会话，不传 Cookie、请求头或密钥。

## 快速开始

需要 Go 1.27 或更新版本。

```sh
go test ./...
cd desktop && go test ./...
```

哪些检查覆盖了什么、哪些只能在真窗口里跑，见[验证索引](docs/development/VERIFICATION.md)。

桌面端（Windows，会打开真实窗口）：

```sh
cd desktop
go build .
./desktop.exe -media <本地文件或 http(s) 地址> [-media <第二路> ...]
```

Web 端：

```sh
go run ./web -media-root <媒体目录> \
  -media /media/hls-vod/index.m3u8 -media /media/hls-vod/second.m3u8
```

它会打印一个带令牌的 `http://127.0.0.1:<端口>/?token=<令牌>`；令牌只出现这一次，页面立刻把它换成 HttpOnly cookie 并从地址栏移除。

## 边界

- 运行中也能导入源：卡片出现后播放器随即出画，失败的地址只报错、不留下空卡片。
- 回看还停在「切到一段录像」：切换先把新播放器播出画面再换源，失败回滚；把回看窗口当作可 Seek 的授权区间还没做。
- 桌面端只推进正在被绘制的那一路：切到别的页面，舞台会停，切回来几秒后追上。
- 音频分离与内容对齐都是实验脚本，不在播放路径上，也不会自己改时间轴。
- Web 端不做对轴、不做回看；Seek 只落在浏览器已缓冲的范围内。
- 自动对轴的视频路径只实现、未用真实录像验证；音频路径有实测数据，细节在 [tools/align](tools/align/README.md)。

## 目录

| 路径 | 内容 |
| --- | --- |
| `core/` | 会话模型、事件时间轴 |
| `core/hls/` | 播放列表读取与探轨 |
| `core/dvr/` | 回看缓冲、缺口与录像拼接 |
| `desktop/` | MyGo 桌面端与场景脚本 |
| `web/` | 回环会话服务与页面 |
| `extension/` | 扩展与 Native Host |
| `tools/` | 分离、对轴与设计令牌生成 |
| `docs/development/` | 架构与工程规范 |

各模块的状态与依赖方向见[架构说明](docs/development/ARCHITECTURE.zh-CN.md)，Web 的令牌、同源与路径限制也在那里。

## 许可证

MIT，见 [LICENSE](LICENSE)。
