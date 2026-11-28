# 更新日志 / Changelog

本文件只记录已经实现、与使用或兼容性有关的变化。GitHub Release 的正文从对应 Tag 的版本章节生成，不另写一份摘要，也不记录开发流水或未完成计划。

This changelog records completed changes that matter to someone using MuxShift. GitHub Release notes come from the matching tagged section, not a separately maintained summary.

## [0.0.5] - 2026-11-28

### 简体中文

**新增**

- 桌面端分别保存各舞台的布局、主画面和音频分配。来源面板可逐路显示、暂停和恢复播放。
- 新来源默认归导入舞台，移出该舞台即删除。显式共享后，其他舞台可从目录添加，解除舞台引用时仍保留在目录中。
- 会话重启后恢复各舞台的分配。带凭据、查询参数或片段的 HTTP(S) 地址会省略，保留来源供重新选址。
- 用 `-media` 打开媒体时保留已保存的会话。
- 来源和场景可自定义名称。
- 自由布局可摆放所有已显示的画面。选中来源后可直接拖移、缩放或锁定摆位。
- 来源可单独刷新。支持多个可播档位的直播主播放列表可逐路选择清晰度。
- 浏览器扩展的候选地址可以逐条展开，复制完整链接后可在桌面端手动添加。

**改进**

- 应用导航收为「舞台」「诊断」「设置」，来源、混音和同步在舞台控制台内操作。
- 场景栏可以收起再展开，回放缓存限额可以直接输入。同步面板在顶部显示估算结果和来源编号，目标偏移变化后会隐藏旧建议。
- 窄窗中的播放栏使用紧凑图标，为单行播放时间留出空间。
- 配色、字号、字体、紧凑间距和画布下方的时间线开关即时应用并保存。

**修复**

- 点击编辑位置时会选中一个可见画面，短窗口也能操作下沿的缩放把手。
- 来源按保存顺序恢复，临时打不开时可取消恢复并保留原有分配。拖动音量等连续保存不会再因两个计时器同时写入而卡住。
- 同一个会话文件只允许一个窗口保存，避免旧窗口覆盖较新的舞台布置。
- 离线来源保留待恢复的舞台摆位和音视频分配。
- 启动优先打开当前舞台使用的来源。切走后停用新舞台不再使用的播放器。
- 检测媒体地址、探测 HLS 轨道和下载回放分片时，会校验公网请求的实际连接地址。桌面端拒绝超过 1 MiB 的 HLS 播放列表，HLS 诊断日志省略 URL 中的凭据和签名。
- 选择清晰度或刷新来源后，清晰度菜单会收起。取消读取后，旧结果不会覆盖新菜单。
- 原设置文件损坏时，外观更改只在本次运行中生效。保存失败后，可以重新点击「应用」。
- 对齐估算会计入参考来源的偏移。回放微调若早于录像首帧，既有偏移保持原值。
- 舞台播放控制以当前舞台为范围，只有舞台包含活动同步组成员时才会控制该组。观看模式的播放按钮和空格键使用同一规则。
- 浏览器扩展检测到换页或重载时会清空旧候选，并提示重新扫描。
- 扩展只在 B 站直播间解析直播地址，保留分线路地址的签名。YouTube 解析会核对当前视频或直播频道。
- 页面探测未能连接直播候选时，扩展会保留一个未验证的地址，供用户手动发送。发送超时时，扩展会提示先查看桌面端的来源面板。
- 关闭窗口会取消尚未完成的回放准备。播放器重开期间，回放不会覆盖仍在输出的画面。

**兼容性**

- 公网 HTTP 播放列表需直连或使用 SOCKS5。HTTPS 播放列表仍支持 CONNECT。
- 未对齐静音保护默认关闭，可在「设置 → 播放」开启。

### English

**Added**

- The desktop saves each stage's layout, primary picture, and audio routing. The Sources panel can show, pause, and resume individual sources.
- New sources belong to the importing stage and are deleted when removed from that stage. Explicitly shared sources can be added from the catalog in other stages and remain in the catalog when unlinked.
- A restart restores each stage's assignments. Session files omit HTTP(S) addresses with credentials, a query, or a fragment, while retaining their sources for address selection.
- Launching with `-media` preserves the saved session.
- Sources and scenes can have custom names.
- Free layout places every shown picture. Select a source to move, resize, or lock its position directly.
- Refresh sources individually. A live master playlist with multiple playable variants offers a per-source quality choice.
- Expand any candidate in the browser extension to copy its full link for manual import in the desktop app.

**Improved**

- Application navigation uses Stage, Diagnostics, and Settings. Sources, Mixer, and Sync live in the stage console.
- Collapse or reopen the scene rail and type replay cache limits directly. The Sync panel shows estimates and source numbers at the top, and hides an old suggestion when the target offset changes.
- The playback bar uses compact icons in narrow windows to keep the playback time on one line.
- Theme, text size, font, compact spacing, and the timeline toggle under the stage apply and save immediately.

**Fixed**

- Position editing selects a visible picture and keeps the bottom resize handles reachable in short windows.
- Sources reopen in the saved order, and cancelling recovery keeps the original assignments. Overlapping autosaves no longer deadlock during continuous changes such as volume drags.
- Only one window can save a given session file, so an older window cannot overwrite a newer stage arrangement.
- Unavailable sources retain pending stage placement and audio and video assignments.
- Startup opens sources used by the current stage first. Stage changes stop players the new stage no longer uses.
- Media detection, HLS track probes, and replay segment downloads validate the connection address for public requests. The desktop rejects HLS playlists larger than 1 MiB, and HLS diagnostic logs omit URL credentials and signatures.
- Choosing a quality level or refreshing the source closes the quality menu. Canceled requests cannot overwrite the reopened menu.
- Appearance changes apply for the current run when the original settings file is damaged. Use Apply to retry a failed save.
- Alignment estimates include the reference source's offset. Replay nudges before the recording's first frame leave the existing offset intact.
- Stage transport controls the current stage and includes the active sync group only when the stage has a member in that group. The viewing controls and Space shortcut use the same rule.
- The extension clears old candidates and asks for a fresh scan when it detects a page change or reload.
- The extension resolves Bilibili streams only in live rooms and keeps mirror signatures. YouTube resolution checks the current video or live channel.
- If the page cannot reach a live candidate, the extension offers an unverified address for manual handoff. If handoff times out, it asks you to check Sources in the desktop app before retrying.
- Closing the window cancels replay preparation. During a player reload, replay cannot replace the picture that is still playing.

**Compatibility**

- Public HTTP playlists require a direct connection or SOCKS5. HTTPS playlists also support CONNECT.
- Unaligned mute protection is off by default. Enable it in Settings → Playback.

## [0.0.4] - 2026-11-22

### 简体中文

**新增**

- 设置 → 关于新增「浏览器扩展」小节：显示随程序打包的扩展目录，一键在文件管理器中打开，并给出加载解包扩展与配对的操作步骤。

**改进**

- 安装目录重整为三段：`muxshift.exe`、许可证与 `VERSION.txt` 在顶层，浏览器扩展归入 `extension\`，用户文档归入 `docs\`。安装器与便携 ZIP 采用同一结构。

**修复**

- 安装器与便携 ZIP 现在真正带上浏览器扩展（`extension\`）。此前发布产物没有任何扩展文件，README 让用户加载 `extension\` 却无处可找，网页地址因此无法导入桌面端。

### English

**Added**

- Settings → About has a new "Browser extension" section: it shows the extension folder shipped beside the program, opens that folder in the file manager, and lists the steps to load the unpacked extension and pair.

**Improved**

- The install layout is now three parts: `muxshift.exe` with the licence and `VERSION.txt` at the top, the browser extension under `extension\`, and the user documents under `docs\`. The installer and the portable ZIP share this layout.

**Fixed**

- The installer and the portable ZIP now actually carry the browser extension (`extension\`). Earlier releases shipped no extension files at all while the README told users to load `extension\`, so a website address could never reach the desktop window.

## [0.0.3] - 2026-11-22

### 简体中文

**新增**

- 设置新增「关于」：显示 MuxShift 版本与构建提交、Windows 版本与架构、逻辑处理器、物理内存和设置文件路径。
- 关于页可以检查更新：打开时查询一次 GitHub 最新发布，失败后可以手动重试；发现新版本时显示版本号与发布页入口。

**改进**

- Windows 发布包改用 GUI 子系统构建，正常启动不再带一个控制台窗口；从终端启动时日志仍附着到父控制台。
- 发布新增按当前用户安装的安装器：创建开始菜单快捷方式、可选的桌面快捷方式和卸载项，同时保留免安装 ZIP。

**兼容性**

- 更新检查需要访问 `api.github.com`；未登录请求受 GitHub 速率限制，检查失败不会重试或自动下载。

### English

**Added**

- Settings now has an About section: MuxShift version and build commit, Windows version and architecture, logical processors, physical memory, and the settings file path.
- About can check for updates: opening it asks GitHub once for the latest published release, with a manual retry after a failure; a newer version shows its number and a link to the release page.

**Improved**

- Windows release builds now use the GUI subsystem, so a normal start no longer carries a console window; started from a terminal, logs still attach to the parent console.
- Releases now include a per-user installer that creates Start Menu and optional desktop shortcuts and an uninstall entry, while the portable ZIP remains available.

**Compatibility**

- Update checks need `api.github.com`; unauthenticated requests are rate-limited by GitHub, and a failed check neither retries by itself nor downloads anything.

## [0.0.2] - 2026-11-22

### 简体中文

**新增**

- 桌面端工作台改为场景栏、画布和控制台：Sources / Mixer / Sync 三块面板同时可见，可拖高、折叠、单独隐藏，也可拖动面板之间的分隔；窄窗口退化为三页 Tabs，设置里可切换成每源一行。
- 增加影院、全屏与置顶紧凑小窗三种观看方式；来回切换不重开播放器、不跳时钟，退出时恢复窗口界限、最大化状态与打开的面板。
- 舞台支持五种布局：Focus、Split、PiP、Grid、Hero；切换只调整各画面的位置，不重开播放器，也不动声音和同步。
- 编辑模式下画面可以拖动、八向缩放、锁定比例、填精确数值、方向键微调，并调整置顶与叠放顺序；位置按舞台比例保存，窗口缩放后仍然有效。
- 空舞台直接给出「打开来源」与「去来源面板」两条起始动作。
- 顶部 `+ 来源` 与控制台来源面板的 `+` 打开同一个添加面板：本地文件、媒体链接、网站地址。
- 本地文件走系统文件选择器；取消不改变会话。
- 媒体链接按实际内容判断播放列表或可直读媒体；网页、404 与不支持的类型明确拒绝，失败不留空卡片。
- 网站地址用系统浏览器打开；浏览器扩展与桌面窗口通过一次性配对码把页面已加载的地址传给桌面窗口，不再需要 Native Host。
- 扩展在 B 站、Twitch、Kick 和 YouTube 的直播页上可以取出一条签名地址。B 站、Twitch、Kick 可直接播放；YouTube 的地址与出口 IP 绑定，分片可能被拒绝，扩展与桌面会如实报错。
- 设置新增外观页：午夜、石墨、日光、青瓷、纯黑五套配色，三档字号，字体选择（跟随系统与四种字体），界面语言与更紧凑的间距；选择立即预览，应用后写入设置。

**改进**

- 桌面端统一颜色、间距、字号、圆角与焦点状态；来源行、混音通道和同步卡片按类型使用不同的表面，减少悬空文字。
- 检查器改用动作与状态的说法（`playing`、`held at 3.2s`、`muted`、`silent: volume is zero`、`not yet: no common clock found`），Core id、代次与播放器句柄移到诊断页。
- 窄窗口的舞台角落可以直接打开属性抽屉和页面菜单；单行控制台的来源列表可以滚动，导入过程中在状态栏旁可以取消。
- 直播 master 使用独立音频 rendition 时会明确拒绝，不再把只有视频的地址当作完整音视频来源；播放列表的相对地址按重定向后的地址解析。
- Windows 的 HTTP 与 HTTPS 代理分别应用，已经带协议的代理地址不会重复添加协议。

**修复**

- 打开失败的本地或网络来源不再留下幽灵 Source/Track。
- 引擎在打开成功之后才报告的失败会被收回，窗口不再保留一个永远不能播的源。
- 读地址期间关闭窗口不再把结果落进会话。
- 状态栏、错误报告和日志只显示脱敏后的地址；重试使用失败记录里的地址，不再读取后来输入的草稿。
- 新增场景不再清空其他场景的编辑记录；删除不可达来源时同时清空它的轨道 ID。
- 移动或锁定画面不再改变原有堆叠；媒体宽高比按舞台坐标换算；拖动后位置字段重新读取；方向键只在编辑模式下移动画面。

**兼容性**

- Windows 的 `ProxyOverride` 排除列表尚未应用；扩展只支持默认的 `127.0.0.1:8731` 配对端口，其他 `-handoff-port` 值会在启动时拒绝。

### English

**Added**

- The desktop workbench now has a scene rail, the canvas and a console: the Sources / Mixer / Sync panels are visible together, can be made taller, folded, hidden one at a time, and resized from the seams between them. A narrow window turns them into three tabs, and a setting switches to one row per source.
- Cinema, full screen and an always-on-top compact window join the viewing modes. Moving between them never reopens a player or jumps the clock, and leaving one restores the old window bounds, maximized state and open panels.
- The stage supports five layouts: Focus, Split, PiP, Grid and Hero. Switching one only moves pictures; players, sound and sync are untouched.
- In editing mode a picture can be dragged, resized from eight handles, locked to its aspect ratio, placed by exact numbers, nudged with the arrow keys, and raised or lowered in the stack. Positions are stored as fractions of the stage, so they survive a window resize.
- An empty stage offers two starting actions: open a source, or go to the sources panel.
- The top bar's `+ Source` and the console source panel's `+` open one add-a-source panel with three choices: local file, media link, website address.
- A local file opens the system file picker; cancelling changes nothing.
- A media link is classified by what the address actually serves, so a web page, a 404 or an unsupported type is refused with a reason and leaves no empty card.
- A website address opens in the system browser; a one-time pairing code connects the browser extension to the desktop window and hands back the address the page already loaded, with no Native Host.
- On Bilibili, Twitch and Kick live pages and YouTube live pages, the extension can obtain one signed address; Bilibili, Twitch and Kick can play it directly, while a YouTube address is bound to the egress IP used to obtain it, so only the playlist may load and the segments may be refused; the extension and the desktop report that instead of pretending to play.
- Settings gained an Appearance page: five themes (Midnight, Graphite, Daylight, Celadon, Noir), three text sizes, font selection (System plus four families), the interface language and tighter spacing. A choice previews at once and is written to settings on Apply.

**Improved**

- The desktop uses one set of colours, spacing, type sizes, radii and focus states; source rows, mixer channels and sync cards use surfaces that match their job, so fewer labels float on the page.
- The inspector now speaks in actions and states (`playing`, `held at 3.2s`, `muted`, `silent: volume is zero`, `not yet: no common clock found`); Core ids, generations and player handles moved to the diagnostics page.
- A narrow stage corner opens the properties drawer and the page menu; the one-row console scrolls, and an import can be cancelled beside its status.
- A live master with a separate audio rendition is refused instead of treating its video-only address as a complete source; relative playlist addresses resolve against the final redirect.
- Windows HTTP and HTTPS proxies are applied separately, and an address that already has a scheme is not given a second one.

**Fixed**

- A source whose player cannot open no longer leaves a ghost source and track behind.
- A failure the engine reports only after a successful open is taken back instead of keeping a source that can never play.
- Closing the window while an address is being read no longer lands the result in the session.
- The status bar, error report and log show only redacted addresses; Retry uses the failed record, not whatever draft was typed afterwards.
- Creating a scene keeps the other scenes' edit records; dropping an unreachable source clears its track ids too.
- Moving or locking a picture no longer changes the stack; the media aspect is converted to stage coordinates; dragged fields reload; arrow keys move a picture only in editing mode.

**Compatibility**

- The Windows `ProxyOverride` exclusion list is not applied yet; the extension supports the default `127.0.0.1:8731` pairing port only, and another `-handoff-port` value is refused at startup.


## [0.0.1] - 2026-11-14

### 简体中文

**新增**

- 桌面端以 MyGo 原生窗口同时播放多路 HLS 或本地媒体，由场景决定看哪一路、听哪几路，每路音量独立。
- HLS 接入能读播放列表、探测轨道与首个样本时间；跨流对齐需要播放列表里的 PDT，没有就明确拒绝。
- 回看缓冲把直播分片保存在有界目录里，报告真实可回看区间与缺口，并把一段连续区间切成录像。切换时先让新播放器画出画面再换源，失败则回滚。
- 已经处于回放的舞台可以在 inspector 里按「候选共同窗口内第几秒」请求一次 Seek：逐源授权，越界与并发都被拒绝，提交失败整体回滚。账户只声明位置被接受，不声称目标帧已呈现。
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
