# 维护者发行说明

MuxShift 只有一个发行产品：Windows amd64 桌面版 ZIP。版本、更新记录与发布资产一一对应，任一环节对不上就不发。

| 项目 | 内容 |
| --- | --- |
| 版本来源 | 仓库根目录 `VERSION`（`x.y.z`），`CHANGELOG.md` 的同名版本章节 |
| 标签 | annotated tag `vX.Y.Z` |
| 发布渠道 | GitHub Releases |
| 正式资产 | `muxshift-<version>-windows-amd64.zip`，内含 `VERSION.txt`、`THIRD-PARTY.md` 与校验值 |

## 发行流程

1. **版本准备随功能或修复的 PR 一起完成，不单独开版本 PR。** 同一提交里更新 `VERSION`、`CHANGELOG.md` 的双语条目，如果版本号变化还要运行 `scripts\winres.ps1` 重新生成并提交 `desktop\rsrc_windows_amd64.syso`。合入 `dev`、再经审查合入 `main` 后，该提交才具备发布资格。
2. **打标签是独立的一步：运行 `tag.yml`（手动触发，输入精确版本）。** 它只核对并打标签，不构建任何产物：`VERSION` 与输入一致、目标标签不存在、提交等于当前 `main` 头部，并等该提交的 `main` push CI 变为绿色。任一项不满足就失败关闭，不做自动版本提交、不强制打标签、不跳过检查。
3. **发布是另一独立步骤：标签存在后运行 `release.yml`（手动触发，同一版本）。** 它先确认标签已存在、`VERSION` 与标签一致，再检出该标签构建，跑 `scripts\verify-release.ps1`：从两个不同目录各克隆一次并构建，要求字节一致（`-trimpath`、关闭 cgo）、二进制自述提交与标签提交相同、不含构建机器路径、PE 资源里有图标与版本块、版本块与 `VERSION` 一致。`release.yml` 从不创建或移动标签。
4. **生成 Release 正文。** 正文由 `scripts\release-notes.ps1 -Tag vX.Y.Z` 从 `CHANGELOG.md` 提取，标题只写 `MuxShift vX.Y.Z`；正文分 `### 简体中文` 与 `### English` 两个区块，安装、运行要求与校验信息放进对应语言区块，不在末尾追加中英混排内容。
5. **创建 Release 草稿并核对后再公开。** 草稿里应只有本次真实产出的 ZIP 与 `VERSION.txt`，SHA-256 与文件一致。公开后不移动标签、不替换二进制；说明有误只改正文。

## 更新记录规则

- `CHANGELOG.md` 只记录已经实现、与使用或兼容性有关的变化，不记录开发流水、未完成计划或路线图。
- `[Unreleased]` 随功能累积；发行时整体移入 `[X.Y.Z] - YYYY-MM-DD`，中英文语义必须一致。
- 分类按需使用 **新增 / 改进 / 修复 / 安全 / 兼容性 / 移除**，不预留空分类；脚本会拒绝空分类、模板注释和 `TODO`/`TBD` 占位。
- 发布说明描述可观察的行为，不把「位置已落定」写成「帧已呈现」，不把实验性能力写成产品能力。

## 本地构建与校验

```powershell
scripts\winres.ps1          # 版本变了才需要，产物要提交
scripts\release.ps1         # 产出 dist\muxshift-<version>-windows-amd64
scripts\verify-release.ps1  # 干净克隆、双目录构建、资源与路径检查
```

`release.ps1` 拒绝改动未提交的工作树（除非 `-AllowDirty`，那样产物的版本号会带 `+dirty`），也拒绝版本块与 `VERSION` 不一致的构建。

## 失败与回退

- **构建或校验失败**：不发布。修正后走新版本、新标签，不重试覆盖。
- **资产缺漏或内容不符**：删除草稿 Release 与标签，修复后重新走流程；已公开的 Release 不删改二进制。
- **说明有误**：只修改 Release 正文并注明更正，标签与资产保持不变。
- **用户侧回退**：安装上一个已发布版本即可，配置文件在用户目录、不随回退删除；不保证旧版本能读取新版本写出的配置，回退前先备份。

## 安全要求

- 仓库与发布说明里不出现个人绝对路径、局域网地址、令牌或凭据。
- 构建产物（`dist/`）不入库；`VERSION.txt` 只写版本、提交、构建时间、SHA-256 与 `-version` 输出。
- 发布日志只保留判断发布状态所需的非敏感证据。
