# MuxShift

MuxShift 是一个面向多路音视频组合与时间轴控制的开源项目。

仓库目前是工程骨架，已实现 Go Core 的会话配置模型、事件时间轴、从 HLS 播放列表读取轨道与时间信息、有界的直播回看缓冲，以及一个用 MyGo 原生播放的桌面实验应用：它在同一个会话里登记多个媒体源，由场景决定显示哪路画面、听见哪路声音。尚未提供媒体采集、跨源同步、混音或音频分离功能，也还不能把录下来的窗口真的放出来。

[English](README.en.md)

## 目录

- `core/`：共享 Go Core 的会话配置模型与事件时间轴。
- `core/hls/`：读取 HLS 播放列表、轨道与首个样本时间，不转码、不对外供流。
- `core/dvr/`：把一个直播源的分片存进有界目录，报告真实可回看区间与缺口，并生成录制播放列表。
- `desktop/`：MyGo 桌面实验应用，把多个本地文件或 http(s) 地址登记进一个 Core 会话，按场景选择画面与声音，管理播放器生命周期，并为直播源保留有限历史窗口。
- `web/`：Web 客户端预留位置。
- `extension/`：浏览器扩展预留位置。
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

界面上的按钮选择显示哪路画面、听见哪路声音，调整各自的音量与静音，并把某一路按 ±0.1 秒对到另一路上；未对过时间的源默认静音，界面上的按钮可以放开或撤回。「Pause all / Play all」按同步组的成员下发，不作用于组外的源。`-dvr-dir`、`-dvr-retention`、`-dvr-max-bytes` 打开回看缓冲。`-scenario` 可以跑脚本化的检查（见 `desktop/scenarios.go`，例如 `sync` 是手动对轴、`syncmap` 是单源定位测量、`syncfail` 是成员失败上报、`dvr` 是回看窗口）。它的播放结果来自真实窗口的截图、日志与输出设备电平采样，不进入单元测试；回看窗口目前只有缓冲、共同区间与缓存回收成立，把录制放出画面这一步没有成立，细节见 `testplace/evidence/P8/evidence.md`（本地证据，不进入版本控制）。

## 许可证

见 [LICENSE](LICENSE)。
