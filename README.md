# MuxShift

MuxShift 是一个面向多路音视频组合与时间轴控制的开源项目。

仓库目前是工程骨架，已实现 Go Core 的会话配置模型、事件时间轴、从 HLS 播放列表读取轨道与时间信息，以及一个用 MyGo 原生播放单个媒体源的桌面实验应用：尚未提供媒体采集、跨源同步、混音或音频分离功能。

[English](README.en.md)

## 目录

- `core/`：共享 Go Core 的会话配置模型与事件时间轴。
- `core/hls/`：读取 HLS 播放列表、轨道与首个样本时间，不转码、不对外供流。
- `desktop/`：MyGo 桌面实验应用，播放单个本地文件或 http(s) 地址并管理播放器生命周期。
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

这条命令运行 `core` 的会话与时间轴测试，以及 `core/hls` 对录制样例的读取测试；它不验证媒体功能。

桌面应用需要在 Windows 上单独构建与运行，并会打开一个真实窗口：

```sh
cd desktop
go build .
./desktop.exe -media <本地文件或 http(s) 地址>
```

它的播放结果来自人工观察，不进入单元测试。

## 许可证

见 [LICENSE](LICENSE)。
