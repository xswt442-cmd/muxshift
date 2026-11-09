# MuxShift

MuxShift 是一个面向多路音视频组合与时间轴控制的开源项目。

仓库目前是工程骨架，已实现 Go Core 的会话配置模型与事件时间轴：尚未提供可运行的播放器、媒体采集、按时间对齐的同步或音频分离功能。

[English](README.en.md)

## 目录

- `core/`：共享 Go Core 的会话配置模型与事件时间轴。
- `desktop/`：基于 MyGo 的桌面客户端预留位置。
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

这条命令运行 `core` 的会话测试；不验证媒体功能。

## 许可证

见 [LICENSE](LICENSE)。
