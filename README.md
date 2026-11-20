<div align="center">
  <img src="web/icon.svg" alt="MuxShift" width="88" height="88">
  <h1>MuxShift</h1>
  <p>把多路音视频放进同一个会话，由场景决定看哪路、听哪路，并在事件时间轴上对齐它们</p>
  <p>
    <a href="README.en.md">English</a> ·
    <a href="https://github.com/xswt442-cmd/muxshift/releases">下载</a> ·
    <a href="CHANGELOG.md">更新记录</a> ·
    <a href="docs/development/RELEASE.md">发版流程</a>
  </p>
  <p>
    <a href="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml/badge.svg?branch=dev"></a>
    <a href="https://github.com/xswt442-cmd/muxshift/releases"><img alt="release" src="https://img.shields.io/badge/release-v0.0.1-5d77f5"></a>
    <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-5d77f5"></a>
    <a href="go.mod"><img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-5d77f5"></a>
  </p>
</div>

早期原型：能接入、能切换、能对轴、能在已经回放的舞台上定位。采集、混音、跨源自动同步没有做。

## 下载

[Releases](https://github.com/xswt442-cmd/muxshift/releases) 提供 Windows amd64 ZIP（`muxshift-0.0.1-windows-amd64.zip`），解压后运行 `muxshift.exe`。需要 Windows 10/11，播放依赖系统自带的 Media Foundation。包里带 `VERSION.txt`、`THIRD-PARTY.md` 与校验值；源码构建见[快速开始](#快速开始)。

## 功能

- **一个 Core 会话**：源、轨道、同步组、场景、输出，加一条事件时间轴——共享事件时间加上每条轨道自己的偏移，就是这条轨道该播的媒体时间。
- **接入 HLS**：读播放列表、探测轨道与首个样本时间。有 PDT 才能跨流对齐，没有就明确拒绝，不猜。
- **桌面端**：MyGo 原生窗口播放多路源，场景切画面与声音，每路独立音量；手动对轴 ±0.1 秒，自动对轴只给建议、不写时间轴。左栏只放场景，画布右上角是紧凑图标组，底部 Sources / Mixer / Sync 三块面板并列、可拖高、可收起、可单独隐藏，窄窗退化为三页 Tabs；B（每源一行）是设置项。
- **观看模式**：编辑之外还有影院（保留窗口框架、隐藏编辑界面）、F11 全屏、可置顶的紧凑小窗。四种状态来回切换不重开播放器、不跳时钟；退出时恢复原来的窗口界限、最大化状态与打开的面板。浮层 2.5 秒无操作收起，指针或键盘焦点停在上面时不会消失；F11、Esc、双击画面、Tab、空格、↑↓ 都能完成同样的操作。
- **五种画面布局**：Focus 一次看一路，Split 左右各一路，PiP 一路铺满、另一路在右下角，Grid 是 2×2，Hero 是左侧主画面加右侧竖排。布局条上直接切；切布局只改落位，不重开播放器、不动声音与同步。放不下的源不会退回整个舞台，首帧未到的格子显示「waiting for the first picture」。
- **自由摆放（OBS 式的源变换）**：编辑状态下选中画面可以拖动、八向缩放、锁定比例，也能在检查器里填精确的位置与大小、用方向键微调；置顶/置底/上移/下移决定遮挡关系。编辑只在编辑状态生效，看着的时候画面不会被误拖。位置按舞台比例保存，窗口大小和缩放变了也不跑位。
- **回看缓冲**：直播分片存进有界目录，报告真实可回看区间与缺口，把一段连续区间切成录像；切换先把新播放器播出画面、再换源，失败回滚。
- **在回放里定位**：已经进入回放的舞台可以在 inspector 里按「共同窗口内第几秒」请求一次 Seek；逐源授权，越界与并发都明确拒绝，提交失败整体回滚。
- **Web 端与扩展**：Web 端只绑定回环地址、用 hls.js 播放、能切场景；扩展把用户点选的那一个地址交给本机会话，不传 Cookie、请求头或密钥。

## 快速开始

需要 Go 1.27 或更新版本。

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

它会打印一个带令牌的 `http://127.0.0.1:<端口>/?token=<令牌>`；令牌只出现这一次，页面立刻把它换成 HttpOnly cookie 并从地址栏移除。

## 添加来源

顶部 `Open…` 与 Sources 页的 `Add…` 打开同一个面板，三选一：

- **本地文件**：调用系统文件选择器，选中后加入当前会话；取消不改变任何东西。
- **媒体链接**：http(s) 的 HLS 播放列表或可直接读取的媒体文件。桌面端按内容而不是扩展名判断，网页、404 与不支持的类型都会明确拒绝，失败不留空卡片。
- **网站地址**：用系统浏览器打开页面。要让页面里的地址进入桌面端，需要 `extension/` 里的扩展（解压加载，不再需要 Native Host）：在 Diagnostics 页取得一次性配对码，打开扩展弹窗粘贴配对，之后选中页面已加载的那一条地址即可交回本窗口。
- **直播**：B 站、Twitch、Kick 的直播页，以及 YouTube 的直播页上，弹窗会多出 `Use this live stream`。地址是在页面会话里换出来的（B 站 `platform=h5` 的 m3u8、Twitch 的播放令牌、Kick 频道接口里的播放地址、YouTube 页面自己的 `hlsManifestUrl`），桌面端拿到的只有一条签名 URL，没有 Cookie。站点各自的取法写成一张表：每站只描述「哪个页面、问哪个接口、答案里的哪个字段是地址」，公共部分（请求、取第一个可达镜像、HLS 判定、说明文案）只有一份，加一站就是加一条记录。桌面端按播放列表内容判断：主播放列表选一档唯一可放的变体，带 `#EXT-X-KEY` 的加密流、没有 `#EXT-X-ENDLIST` 的直播流都识别得出来，前者明确拒绝，后者直接作为直播打开，并由它自己开始播放——不需要按 Play。

能否交回取决于页面自己：`blob:`/MSE、需要登录或 DRM、跨源受限的页面不会被绕过，只会给出说明。只接收 http(s) 地址，不传 Cookie、请求头或密钥；直播地址的鉴权来自签名本身。Twitch 的令牌只能是匿名令牌——该站不允许跨源请求携带凭据，公开频道可直接播放，需要订阅或地区权限的频道会如实拒绝。

## 文档

| 文档 | 内容 |
| --- | --- |
| [架构](docs/development/ARCHITECTURE.zh-CN.md) | 模块边界、数据流、回放时钟契约与已知限制 |
| [验证索引](docs/development/VERIFICATION.md) | 哪些检查覆盖了什么、哪些只能在真窗口里跑 |
| [更新记录](CHANGELOG.md) | 每个版本新增、改进、修复了什么 |
| [发版流程](docs/development/RELEASE.md) | 维护者：版本、标签、构建校验与发布步骤 |

## 边界

- **从直播进入回放不提供**：没有工具栏按钮，也没有快捷键。内部规划与事务代码存在，但由测试守着，出货文件里没有调用点。
- **跨源内容对应只是实验**：V1 已被判定不能用于授权，V2 仅记录为 ObservedSupport；它们不在播放路径上，也不会自己改时间轴。
- **「位置已落定」不等于「目标帧已呈现」**：Seek 提交只证明播放器报告了请求的位置，账户文案保持 `position settled, the frame is not confirmed`。
- 桌面端只推进正在被绘制的那一路：切到别的页面，舞台会停，切回来几秒后追上。
- 回看边界的首次定位可能间歇失败并安全回滚，频率尚未量化；Web 端不做对轴与回看，Seek 只落在浏览器已缓冲的范围。
- 运行中导入的源要等播放器出画才谈得上同步；失败地址只报错，不留下空卡片。
- **视频网站不是已支持的功能**：扩展只交出页面已经加载好的 http(s) 媒体地址，加上四个站点直播页上换出的那条地址。B 站这类把分片交给 MSE 的点播页面拿不到单个可重放地址；YouTube 的点播样本在未登录时直接要求登录；两者都只给明确提示，不会绕过登录、跨源规则或 DRM。YouTube 直播能换出 `hlsManifestUrl`，但该地址里的 `/ip/<出口 IP>/` 与取地址时的出口绑定：本机实测播放列表可取、媒体分片被边缘拒绝（`403`），所以在这条链路上它落不到画面上，扩展与桌面都会如实报错而不是假装在放。
- **直播只播放「现在」**：没有回看缓冲、不做跨源对齐；导入后共享时钟随画面一起开始，签名地址过期或被换掉后要重新从扩展取一次。
- **网络要走代理的机器**：桌面端先看 `HTTP_PROXY`/`HTTPS_PROXY`，没有就沿用 Windows「Internet 选项」里的代理设置，本机回环地址始终直连。

## 目录

| 路径 | 内容 |
| --- | --- |
| `core/` | 会话模型、事件时间轴、HLS 与回看缓冲 |
| `desktop/` | MyGo 桌面端与场景脚本 |
| `web/`、`extension/` | 回环 Web 会话与浏览器扩展（无需 Native Host） |
| `tools/` | 分离、对轴与设计令牌生成 |
| `docs/development/` | 架构、验证索引、发版流程 |

## 许可证

MIT，见 [LICENSE](LICENSE)。
