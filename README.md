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
    <a href="https://github.com/xswt442-cmd/muxshift/releases"><img alt="release" src="https://img.shields.io/badge/release-v0.0.5-5d77f5"></a>
    <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-5d77f5"></a>
    <a href="go.mod"><img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-5d77f5"></a>
  </p>
</div>

## 下载

[Releases](https://github.com/xswt442-cmd/muxshift/releases) 提供 Windows amd64 安装器（`muxshift-0.0.5-windows-amd64-setup.exe`）和便携 ZIP（`muxshift-0.0.5-windows-amd64.zip`）。安装器按当前用户安装，创建开始菜单快捷方式与可选的桌面快捷方式，并注册卸载项；ZIP 解压后直接运行 `muxshift.exe`。两种方式得到同一套目录：程序与 `VERSION.txt` 在顶层，浏览器扩展在 `extension\`，文档在 `docs\`。两者都是 GUI 程序，正常启动不会带控制台窗口；运行需要 Windows 10/11 和系统自带的 Media Foundation。从源码构建见[快速开始](#快速开始)。

## 能做什么

- **一个会话可以加入多路源**：每路源有自己的音视频轨，场景决定显示哪一路画面、播放哪一路声音，每路音量分别设置。
- **恢复会话**：启动时恢复舞台布置，并打开当前舞台使用的可恢复来源。会话文件省略带凭据、查询参数或片段的 HTTP(S) 地址，但保留来源与舞台分配，可在「舞台 → 来源」重新选择地址。
- **临时会话**：用 `-media` 打开媒体会保留已保存的会话。多个窗口使用同一个会话文件时，只有持有文件锁的窗口可以保存。
- **逐路控制**：在「舞台 → 来源」逐路显示画面、播放、暂停或刷新。直播主播放列表提供多个可播档位时，可为该来源选择清晰度。
- **手动对齐时间**：按 ±0.1 秒的步长调整一路源的偏移，把它对齐到另一路源的事件时间。播放列表带 PDT 时还能得到跨源对齐建议，不过自动比较只给建议，不会修改时间轴。
- **回看直播**：直播分片保存在一个有大小上限的目录里。可以查看还有哪些区间可回看，并把其中一段连续区间切成录像单独播放。
- **回放里跳转**：已经进入回放的舞台，可以请求跳到候选共同回放窗口内的某个秒数。每一路源单独授权，目标越界或前一次请求还没完成时，新请求会被拒绝。
- **画面布局**：提供单路、左右分屏、画中画、网格、主次和自由布局。
- **自由摆放画面**：选中画面即可拖动、缩放、锁定摆位或输入精确数值。各舞台分别保存位置和叠放顺序。
- **四种观看方式**：编辑、影院、F11 全屏、置顶小窗，切换时不会重新加载播放器。
- **外观**：配色、字号、字体、紧凑间距和「在画布下方显示时间线」即时应用并保存。修改「外观」里的控制台布局或「通用」里的语言后，点击「应用」保存。
- **关于**：设置里的「关于」显示当前版本与构建提交、Windows 版本与架构、逻辑处理器、物理内存和设置文件路径；打开时会查询 GitHub 最新发布，也可以手动重试检查更新。
- **Web 端**：只绑定本机回环地址，用 hls.js 播放，可以切换场景。
- **浏览器扩展**：把页面里已经加载的媒体地址交给桌面端，不传 Cookie、请求头或密钥。

## 快速开始

根模块需要 Go 1.27 或更新版本，桌面模块需要 Go 1.27.3 或更新版本。

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

使用代理的机器：桌面端先读 `HTTP_PROXY` 和 `HTTPS_PROXY`。两个变量都没有设置时，沿用 Windows「Internet 选项」里的代理配置，HTTP 与 HTTPS 分开应用；`ProxyOverride` 排除列表尚未应用，本机回环地址始终直连。

程序读取公网媒体元数据时会校验实际连接地址。明文 HTTP 播放列表支持直连或 SOCKS5，HTTPS 播放列表也支持通过 HTTP 或 HTTPS 代理建立 CONNECT 隧道。

## 添加来源

点击舞台来源面板的 `+` 或空舞台上的添加按钮，即可从三种方式中选择。

- **本地文件**：用系统文件选择器选择文件，选中后加入当前会话。取消选择时，会话保持不变。
- **媒体地址**：打开 HTTP(S) 的 HLS 播放列表或可直接读取的媒体文件。桌面端按响应内容判断类型，遇到网页、404 或不支持的类型会报错。
- **网页地址**：在系统浏览器中打开网页。配对后，浏览器扩展可以把网页中的媒体地址交给桌面端。

在「设置 → 关于 → 浏览器扩展」选择「打开目录」，也可以直接打开安装目录下的 `extension` 文件夹。在 `edge://extensions` 或 `chrome://extensions` 启用开发者模式，然后加载未打包的扩展。将诊断页的一次性配对码粘贴到扩展弹窗即可配对，无需 Native Host。关闭桌面窗口后配对失效，下次需使用新的配对码。

候选地址可用键盘选择并发送，也可展开行尾的小三角复制完整链接。支持的直播页提供「解析并发送」，换页或重载后需重新打开弹窗扫描。桌面接收请求后，在「舞台 → 来源」查看播放状态。

B 站直播间及受支持的 Twitch、Kick 和 YouTube 页面可交接解析出的 HTTP(S) 地址，其中可能带有签名。跨源不透明响应只能说明线路有响应，桌面端仍会读取播放列表确认能否打开。

桌面端拒绝用 `#EXT-X-KEY` 或 `#EXT-X-SESSION-KEY` 声明加密的播放列表，`METHOD=NONE` 表示未加密。没有 `#EXT-X-ENDLIST` 的媒体播放列表按直播打开并自动播放。主播放列表使用独立音频列表时，桌面端会拒绝导入，因为当前播放器每次只打开一个地址。

扩展能否取出地址取决于页面：`blob:`/MSE 地址、需要登录或受 DRM 保护的页面、受跨源规则限制的页面都无法绕过，扩展只在弹窗里说明原因。扩展只提交 http(s) 地址，不传 Cookie、请求头或密钥。

## 文档

| 文档 | 内容 |
| --- | --- |
| [架构](docs/development/ARCHITECTURE.zh-CN.md) | 模块边界、数据流、回放时钟契约与已知限制 |
| [验证索引](docs/development/VERIFICATION.md) | 哪些检查覆盖了什么、哪些只能在真窗口里跑 |
| [更新记录](CHANGELOG.md) | 每个版本新增、改进、修复了什么 |
| [发版流程](docs/development/RELEASE.md) | 维护者：版本、标签、构建校验与发布步骤 |

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
