# 更新日志 / Changelog

本文件只记录已经实现、与使用或兼容性有关的变化。GitHub Release 的正文从对应 Tag 的版本章节生成，不另写一份摘要，也不记录开发流水或未完成计划。

This changelog records completed changes that matter to someone using MuxShift. GitHub Release notes come from the matching tagged section, not a separately maintained summary.

## [Unreleased]

### 简体中文

<!-- 只在有真实内容时增加分类：**新增**、**改进**、**修复**、**安全**、**兼容性**、**移除**。不要预留空分类。 -->

### English

<!-- Mirror the Chinese categories and facts: **Added**, **Improved**, **Fixed**, **Security**, **Compatibility**, **Removed**. Omit categories with no items. -->

## [0.0.1] - 2026-11-14

### 简体中文

**新增**

- 桌面端以 MyGo 原生窗口同时播放多路 HLS 或本地媒体，由场景决定看哪一路、听哪几路，每路音量独立。
- HLS 接入能读播放列表、探测轨道与首个样本时间；跨流对齐需要播放列表里的 PDT，没有就明确拒绝。
- 回看缓冲把直播分片存进有界目录，报告真实可回看区间与缺口，并把一段连续区间切成录像；切换先让新播放器画出画面再换源，失败回滚。
- 已经处于回放的舞台可以在 inspector 里按「候选共同窗口内第几秒」请求一次 Seek：逐源授权，越界与并发都被拒绝，提交失败整体回滚；账户只声明位置已落定，不声称目标帧已呈现。
- 手动对轴以 ±0.1 秒步进，自动对轴只给出建议与置信度，不写时间轴。
- 设置页覆盖常规、回放、音频、DVR、界面、快捷键与诊断，写入本机配置目录，重启后恢复工作区与数值。
- Sources 页支持运行中导入地址、选中源、显示与听音、移除，以及失败后的重试。
- Web 端只绑定回环地址，用 hls.js 播放并切换场景；令牌在引导地址出现一次，随即换成 HttpOnly cookie 并从地址栏移除。
- 浏览器扩展与 Native Host 只转交用户点选的那一个地址，不传 Cookie、请求头或密钥。
- 发版以 `VERSION` 为单一版本来源，`THIRD-PARTY.md` 由二进制实际链接的模块生成，构建关闭 cgo 以便同一提交在不同目录产出相同字节。

**修复**

- 运行中导入完成或被拒绝后，Sources 页会立刻重绘，不再需要切换页面才看到新源或错误。
- 暂停的舞台准备回放切换时持续请求重绘，替换播放器因此能画出首帧；此前会一直等到超时并回滚。

### English

**Added**

- The desktop client plays several HLS or local sources in one native MyGo window; scenes decide which one is seen and which are heard, with an independent volume per source.
- HLS intake reads the playlist and probes tracks and the first sample time; cross-stream alignment needs the playlist's PDT and refuses when there is none.
- The DVR buffer keeps live segments in a bounded directory, reports what is really watchable and where the gaps are, and cuts one continuous span into a recording; a switch puts the new player's picture up before replacing the source and rolls back when it cannot.
- A stage that is already replaying can ask its inspector for one position inside the candidate common window: each source is authorized separately, out-of-window and concurrent requests are refused, and a failed commit rolls back entirely. The account says the position settled, never that the frame was presented.
- Manual alignment moves in 0.1 s steps; automatic alignment only proposes an offset and a confidence, and never writes the timeline.
- Settings cover general, playback, audio, DVR, appearance, shortcuts and diagnostics, are written to the user's config directory, and restore the workspace and values on restart.
- The Sources page imports an address while running, selects a source, shows or hears it, removes it, and retries one that failed.
- The web client binds to loopback only, plays with hls.js and switches scenes; the token appears once in the bootstrap URL, is traded for an HttpOnly cookie and removed from the address bar.
- The browser extension and native host forward only the address the user picked, never cookies, headers or keys.
- Releases take their version from `VERSION`, generate `THIRD-PARTY.md` from the modules the binary actually links, and build with cgo off so one commit produces the same bytes in different directories.

**Fixed**

- The Sources page now repaints as soon as an import finishes or is refused, instead of waiting for a page switch to show the new source or the error.
- A paused stage now keeps asking for frames while a replay switch is prepared, so the replacement player can present its first frame; previously the wait ran to its timeout and rolled back.
