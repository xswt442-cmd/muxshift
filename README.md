<div align="center">
  <img src="web/icon.svg" alt="MuxShift" width="88" height="88">
  <h1>MuxShift</h1>
  <p>把多路音视频放进同一个会话，由场景决定看哪路、听哪路，并在事件时间轴上对齐它们</p>
  <p>
    <a href="README.en.md">English</a> ·
    <a href="https://github.com/xswt442-cmd/muxshift/releases">下载</a> ·
    <a href="CHANGELOG.md">更新记录</a>
  </p>
  <p>
    <a href="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml/badge.svg?branch=dev"></a>
    <a href="https://github.com/xswt442-cmd/muxshift/releases"><img alt="release" src="https://img.shields.io/badge/release-v0.0.2-5d77f5"></a>
    <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-5d77f5"></a>
    <a href="go.mod"><img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-5d77f5"></a>
  </p>
</div>

## 下载

[Releases](https://github.com/xswt442-cmd/muxshift/releases) 提供 Windows amd64 ZIP（`muxshift-0.0.2-windows-amd64.zip`），解压后运行 `muxshift.exe`。运行需要 Windows 10/11 和系统自带的 Media Foundation。压缩包里还有 `VERSION.txt`、`THIRD-PARTY.md` 和校验值。从源码构建见[快速开始](#快速开始)。

## 能做什么

- **一个会话可以加入多路源**：每路源有自己的音视频轨，场景决定显示哪一路画面、播放哪一路声音，每路音量分别设置。
- **手动对齐时间**：按 ±0.1 秒的步长调整一路源的偏移，把它对齐到另一路源的事件时间。播放列表带 PDT 时还能得到跨源对齐建议，不过自动比较只给建议，不会修改时间轴。
- **回看直播**：直播分片保存在一个有大小上限的目录里。可以查看还有哪些区间可回看，并把其中一段连续区间切成录像单独播放。
- **回放里跳转**：已经进入回放的舞台，可以请求跳到候选共同回放窗口内的某个秒数。每一路源单独授权，目标越界或前一次请求还没完成时，新请求会被拒绝。
- **五种画面布局**：Focus 单路、Split 左右、PiP 画中画、Grid 2×2、Hero 主画面加侧栏。
- **自由摆放画面**：编辑状态下可以拖动画面、缩放、锁定宽高比、输入精确的位置数值，并调整画面的叠放顺序。位置按舞台比例保存，改变窗口大小时不会错位。
- **四种观看方式**：编辑、影院、F11 全屏、置顶小窗，切换时不会重新加载播放器。
- **外观**：五套配色、三档字号、字体、界面语言和紧凑间距，都在设置里选择，选定后立即预览。
- **Web 端**：只绑定本机回环地址，用 hls.js 播放，可以切换场景。
- **浏览器扩展**：把页面里已经加载的媒体地址交给桌面端，不传 Cookie、请求头或密钥。

## 快速开始

构建需要 Go 1.27 或更新版本。

```sh
go test ./...
cd desktop && go test ./...
```

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

启动后它会打印一个带令牌的地址 `http://127.0.0.1:<端口>/?token=<令牌>`。令牌只在地址里出现一次：服务随即把它换成 HttpOnly cookie，页面立即从地址栏移除令牌。

使用代理的机器：桌面端先读 `HTTP_PROXY` 和 `HTTPS_PROXY`。两个变量都没有设置时，沿用 Windows「Internet 选项」里的代理配置，本机回环地址始终直连。

## 添加来源

`+ 来源` 打开添加面板，可以添加三种来源：

- **本地文件**：用系统文件选择器选择文件，选中后加入当前会话。取消选择时，会话保持不变。
- **媒体链接**：http(s) 的 HLS 播放列表，或可以直接读取的媒体文件。桌面端按响应内容判断类型，不看扩展名，地址返回网页、404 或不支持的类型时都会报错。
- **网站地址**：用系统浏览器打开页面。要让页面里的地址进入桌面端，需要加载 `extension/` 里的扩展，解压加载即可，不需要 Native Host：先在诊断页读取一次性配对码，粘贴到扩展弹窗完成配对，再在弹窗里选中页面已加载的那一条地址。

在 B 站、Twitch、Kick 和 YouTube 的直播页，扩展弹窗会多出 `Use this live stream`。桌面端拿到的是一条签名地址，不带 Cookie。带 `#EXT-X-KEY` 的加密流会被拒绝。播放列表没有 `#EXT-X-ENDLIST` 时，桌面端按直播打开并自动播放。

扩展能否取出地址取决于页面：`blob:`/MSE 地址、需要登录或受 DRM 保护的页面、受跨源规则限制的页面都无法绕过，扩展只在弹窗里说明原因。扩展只提交 http(s) 地址，不传 Cookie、请求头或密钥。

## 文档

| 文档 | 内容 |
| --- | --- |
| [架构](docs/development/ARCHITECTURE.zh-CN.md) | 模块边界、数据流、回放时钟契约与已知限制 |
| [验证索引](docs/development/VERIFICATION.md) | 哪些检查覆盖了什么、哪些只能在真窗口里跑 |
| [更新记录](CHANGELOG.md) | 每个版本新增、改进、修复了什么 |
| [发版流程](docs/development/RELEASE.md) | 维护者：版本、标签、构建校验与发布步骤 |

## 已知限制

- **不支持从直播进入回放**：界面里没有入口。
- **不做采集、混音和跨源自动同步**：只播放给定的地址，不录制屏幕或设备，不混音，也不自动对齐多个来源。
- **视频网站只有 B 站、Twitch、Kick 和 YouTube 的直播页可用**：B 站这类把分片交给 MSE 的点播页拿不到单个可重放的地址，YouTube 的点播页在未登录时要求登录。YouTube 直播的地址绑定获取它时的出口 IP，播放列表可以读取，媒体分片可能被拒绝（403），桌面端只提示失败原因，不绕过登录、跨源规则或 DRM。
- **直播只播当下，Web 端限制更多**：桌面端的直播没有回看缓冲，也不做跨源对齐，签名地址过期后要回到扩展重新获取。Web 端不参与对轴，不保留历史，Seek 只能落在浏览器已缓冲的范围内。
- **桌面端只推进正在显示的那一路**：切到别的页面后，原舞台停止推进。切回来几秒后追上当前时间。
- **回看边界处的第一次跳转可能失败**：刚进入回放时，跳到缓冲区最边缘的位置会间歇失败。失败后保持原来的画面，失败频率尚未统计。

## 目录

| 路径 | 内容 |
| --- | --- |
| `core/` | 会话模型、事件时间轴、HLS 与回看缓冲 |
| `desktop/` | MyGo 桌面端与场景脚本 |
| `web/`、`extension/` | 回环 Web 会话与浏览器扩展（无需 Native Host） |
| `tools/` | 人声分离、自动对轴与设计令牌生成 |
| `docs/development/` | 架构、验证索引、发版流程 |

## 许可证

[MIT](LICENSE)。
