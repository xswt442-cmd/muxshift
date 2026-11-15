# Engineering Standards · 工程化规范

此目录是从维护者的工程规范仓库同步的**项目内规范快照**；用于本仓库独立审阅和验证，不代替实际代码、项目 AGENTS 或 CI。

## 内容导航

- [ENGINEERING_STANDARD.md](ENGINEERING_STANDARD.md)：公开仓库内容准入、文档归属、版本发行、双语 PR 和 Agent 约束。
- [ADOPTION.md](ADOPTION.md)：现有项目接入、验证与后续同步检查。
- [templates/AGENTS.md](templates/AGENTS.md)：仅英文的可移植 Agent 规则模板；必须按项目真实边界调整。
- [templates/CLAUDE.md](templates/CLAUDE.md)：Claude 兼容入口。
- [templates/CHANGELOG.md](templates/CHANGELOG.md)：按「新增 / 改进 / 修复」等类别分组的中英双语 Changelog 样例。
- [templates/PULL_REQUEST_TEMPLATE.md](templates/PULL_REQUEST_TEMPLATE.md)：双语「变更／任务／验证」，允许按复杂度详细解释实现。
- [templates/SECURITY.md](templates/SECURITY.md)：安全策略的权威模板。
- [templates/CONTRIBUTING.md](templates/CONTRIBUTING.md)：贡献指南的权威模板。
- [templates/RELEASE.md](templates/RELEASE.md)：通用发行及回滚操作说明。
- [skills/pr/SKILL.md](skills/pr/SKILL.md)：按项目 PR 模板撰写内容的轻量 PR Skill。
- [scripts/release-notes.mjs](scripts/release-notes.mjs)：从目标版本 Changelog 提取发行说明的无依赖脚本。
- [scripts/sync-pr.mjs](scripts/sync-pr.mjs)：检查或显式同步 PR 模板及 Skill。
- [scripts/sync-community.mjs](scripts/sync-community.mjs)：检查或补建 SECURITY 与 CONTRIBUTING。
- [tests/](tests/)：脚本自动化测试。

## 权责划分

1. **维护者的工程规范仓库**维护通用原则与模板；本目录是独立可读的项目副本，调整后需经过项目审核。
2. **项目仓库**的 `AGENTS.md`、PR 模板、Skill、CHANGELOG、CI 和发行手册必须在本仓库内可独立阅读和执行；项目实现与经验证的现实情况优先于泛用样例。
3. PR：工程规范规定原则，项目的 `.github/PULL_REQUEST_TEMPLATE.md` 规定正文结构，项目的 `.agents/skills/pr/SKILL.md` 负责调查 Diff、解释最终实现和填写真实验证；三者不可各定义一套 PR 格式。
4. **变更传播是显式的。** 修改本目录不自动改变产品仓库；同步后各项目独立审查、测试和提交。严禁直接修改历史 Tag 或二进制发行资产。

## PR 模板与 Skill 同步

从本仓库根目录执行：

```sh
node .github/engineering/scripts/sync-pr.mjs --check /path/to/target-repo
node .github/engineering/scripts/sync-pr.mjs --apply /path/to/target-repo
```

仅管理目标仓库四个 PR 协作文件，不覆盖项目特定的 AGENTS、CHANGELOG 或发布流程。默认检查不写入，发现差异以非零状态退出；显式应用后请查看 Diff 并运行项目自己的检查。修改中的目标文件不会被脚本直接覆盖。

## 社区健康文件同步

`SECURITY.md` 与 `CONTRIBUTING.md` 由 GitHub 按固定名称在 `.github/`、仓库根目录、`docs/` 查找，属于单名文件，不参与双语文档对。权威模板在本目录 `templates/` 下；本仓 `.github/` 下的两份按自身信任边界与验证命令改写。

从本仓库根目录执行：

~~~sh
node .github/engineering/scripts/sync-community.mjs --check .
node .github/engineering/scripts/sync-community.mjs --apply .
~~~

检查文件位置、唯一性与必需章节，不要求与权威模板逐字相同。`--apply` 只补建缺失文件，已有副本一律不覆盖；位置重复或章节缺失交回人工审查。
