---
name: mydocs
description: "Use when organizing or rewriting repository documentation: README, docs/ guides, CHANGELOG and release runbooks. Decide reader tasks and file ownership, verify facts against code/tests, preserve existing commands, limits and safety guarantees during edits, and check README structure and zh/en parity. Do not do Chinese wording or sentence-level rewriting (myshuohua), simple typo fixes, or link repair."
---

# 仓库文档：先分层，再动笔

文档难读，多数情况是这个文件里放了不该放的东西。同样的内容留在 `docs/` 里没问题，搬到首页就碍事。所以顺序固定：先定边界，再抽事实，最后才写句子。

先改词也能过去，但内部细节会换个说法继续待在首页。这份 Skill 管**读者任务、事实完整性和内容分层**，句子的用词与语序交给 `myshuohua`。整理文档不等于不断删字：先保住必要事实，再让读者更容易找到它们。

## 不适用的情况

- 只改错别字、失效链接、过期命令。直接改。
- 内容位置已经对了，读着别扭。那是句子的问题，交给 `myshuohua`。
- 用户要求「润色」一段本来没有信息问题的文字。先说明没有信息增量的改写会造出新的机器味，确认对方要的是重写再动手。

## 安装与更新

这个 Skill 自带安装脚本。源在 `.github/engineering/skills/mydocs/`，Agent 读的是 `.agents/skills/mydocs/`：

```sh
node .github/engineering/skills/mydocs/scripts/install.mjs --check .
node .github/engineering/skills/mydocs/scripts/install.mjs --apply .
```

`--check` 列出不一致的文件并以非零状态退出；`--apply` 写入，遇到有本地改动的路径直接拒绝覆盖。`repo/` 下的内容镜像到仓库根目录（本 Skill 暂无）。`--source` 打印这份副本来自哪个提交和日期。

装好之后，检查器在 `.agents/skills/mydocs/lint-docs.mjs`，规则在同目录的 `rules.json`。不要改装好的副本，改源再 `--apply`。仓库自己的补充规则写进 `AGENTS.md`。

## 第 0 步：这个文件面向谁

| 文件 | 读者 | 允许的内容 |
| --- | --- | --- |
| `README.md` / `README.en.md` | 第一次打开仓库的人 | 是什么、怎么装怎么跑、支持什么、硬限制、指向 `docs/` |
| `docs/` 下的说明 | 已经在用、要看细节的人 | 边界、契约、状态流转、验证范围 |
| `CHANGELOG.md` | 用户与维护者 | 用户能感知的新增、改进、修复、兼容性变化 |
| `docs/development/RELEASE.md` | 维护者 | 可重复的发布、校验、回滚步骤 |

细则和三组真实误放例子见 [references/file-types.md](references/file-types.md)。README 里出现 UI 控件位置、内部状态名、错误处理策略、测试覆盖细节，就是越界。移到 `docs/`，首页留一行链接。

## 第 1 步：抽事实，先不写句子

先识别本文件的读者要完成什么任务，再在仓库的 `testplace/`（Git 忽略）写一份 `docs-facts.md`。不仅列新事实，也记录**旧文档中必须保留的信息和对应的新位置**：

- 安装、构建、运行命令，以及真实的前置依赖与版本
- 支持的输入格式、平台、协议，明确不支持的
- 用户会撞上的硬限制，以及触发条件
- 已有文档的位置，哪些细节应该留在 `docs/`
- 现有文档里仍然准确的操作步骤、默认值、失败行为、兼容/升级限制、数据安全保证及链接；标明是保留、迁移、修正还是删除

不在 README 的事实清单里堆砌这些：仅实现内部有意义的函数名、分支判断顺序、测试编号和未暴露的内部状态。API/ARCHITECTURE 文档需要的符号与状态应保留；用户操作依赖的按钮名称和位置也不能无故删除。

已有文档时，先比对代码、测试与文档；无法确认的冲突应标明待核对，不能靠猜测删掉安全限制或编造操作结果。

## 第 2 步：按事实组织内容

按读者任务安排段落：读者先需要知道什么、随后要做什么、遇到失败时该查哪里。可以用事实清单起草，但凡有疑问仍应回查代码和测试，不能把「写完清单就不翻实现」当成事实核验禁令。硬约束：

- 每个段落必须有信息作用：说明功能、操作、前置条件、限制或指向下一步的链接。删除空泛过渡和重复总结；必要的指路句与安全提示可以保留。
- README 不堆读者用不到的内部标识符；接口名、命令参数、配置键和真实界面名称需要识别或操作时必须准确写出。API 和架构文档按专业读者需要保留实现标识符。
- 不把「严格校验」「如实报错」等空泛承诺当功能。但如果失败条件、数据是否保留、是否回滚、用户是否需要重试会影响决策，就必须写清楚实际行为。
- 不写工程自白：为什么这么设计、哪些代码没被调用、接口怎么在模块间转手。
- 交付前删掉临时 `docs-facts.md`，把需要长期维护的事实放回对应正式文档。

## 第 3 步：README 的边界

- 二级标题只留这些角色：定位、下载或安装、快速开始、用法、已知限制、文档入口、目录、许可。多出来的标题，要么删，要么拆进 `docs/`。
- 一条 bullet 不超过两句。同类限制（各站点、各平台的差异）合成一段，不要逐条列排查记录。
- 功能条目写「能用来干什么」，一条一句。边界条件和实现原理不要写进同一条。
- 功能项和布局项要分开判断。删「面板在哪一栏、按钮在哪个角」之前，先确认这条写的是布局，还是通过这个布局交付的功能。
- 前者删掉，后者改成一句用户视角的功能描述。一条 bullet 同时包含两者时先拆开再删，把功能一起删掉是最常见的误伤。

不设字数上限。按角色裁剪比按字数裁剪稳，安装矩阵长的项目也不会被误伤。

## 第 4 步：跑结构检查

内容放对之后，先确认骨架没走形：

```sh
node .agents/skills/mydocs/lint-docs.mjs README.md README.en.md docs
```

两条规则：

| 规则 | 查什么 |
| --- | --- |
| `readme-headings` | README 的二级标题里有没有内部话题（架构、模块、排查…），总数是否超过 8 个 |
| `zh-en-parity` | `README.md` 与 `README.en.md` 的二级标题、段落、条目数是否对得上 |

`--rules` 看阈值和理由，`--strict` 让 warning 也退 1，`--json` 给 CI 用。`readme-headings` 只提示不拦，命中先判断那段该不该拆进 `docs/`。中英对不上，通常是改了一份忘了另一份。

检查器只管骨架。哪句话怎么写交给 `myshuohua` 的 `lint-words.mjs`：分号、句长、术语写法、固定短语。

## 交付前自检

1. **可用**：第一次访问仓库的人能否找到项目用途、安装方法、主要用法与限制；维护者能否找到相应的内部说明。
2. **保真**：逐项对照事实清单，检查已有的命令、限制、安全保证、适用平台、错误处理与重要链接是否被误删、偷换或错误简化。
3. **分层**：README 是否只保留读者完成任务需要的内容，详细契约是否有稳定文档入口；ARCHITECTURE/API 是否保留必要的内部术语。
4. **语言**：交给 `myshuohua` 清理伪口语、空泛术语和错误搭配，不在本 Skill 里复制另一套语言禁词表。
5. **一致**：执行结构检查，并核对中英两版的行为、限制、步骤与命令，而不只比较条目数量。
