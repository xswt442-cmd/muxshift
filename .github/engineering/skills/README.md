# Skill 安装

`.github/engineering/skills/` 是源，Agent 只在项目里的 `.agents/skills/<name>/` 下发现 Skill。**每个 Skill 自带安装脚本**，装法写在它自己的 `SKILL.md` 里；这里只放全仓一次性操作和三条要求。

三个 Skill 各管一段，互不包含：

| Skill | 管什么 | 判断法 |
| --- | --- | --- |
| `mypr` | PR 描述的结构与模板 | 这个改动该怎么讲给别人看 |
| `mydocs` | 内容放哪个文件、首页留什么 | 这段该不该出现在这个文件里 |
| `myshuohua` | 中文的词、句式、标点 | 这句话正常人会这么说吗 |

`mydocs` 和 `myshuohua` 各带一个检查器，查的东西不重叠：`lint-docs.mjs` 看 README 标题和中英骨架，`lint-words.mjs` 看分号、句长、术语写法、固定短语。判不准时先问「换文件还是换句子」。`myshuohua` 对高置信度的无意义口语、营销词和工程黑话默认报 `error`，对需结合语境判断的句式仍给 `warning` 或 `info`；可用 `--strict` 提高检查力度，但不能直接依据词表自动改写事实。

## 一条命令装全部

```sh
node .github/engineering/scripts/skills.mjs --check .
node .github/engineering/scripts/skills.mjs --apply .
```

`skills.mjs` 只负责遍历 `skills/*/scripts/install.mjs`、转发参数并汇总退出码，新增 Skill 不用改它。想只装一个，直接跑那个 Skill 的脚本：

```sh
node .github/engineering/skills/mypr/scripts/install.mjs --check .
node .github/engineering/skills/myshuohua/scripts/install.mjs --apply .
```

`--source` 打印这份副本来自哪个提交和日期。

## 每个 Skill 都是自足目录

```
skills/mypr/
  SKILL.md                              含「安装与更新」，安装知识在 Skill 里
  scripts/install.mjs                   只认自己所在的目录，不维护路径表
  repo/.github/PULL_REQUEST_TEMPLATE.md 镜像到仓库根目录
  CREDITS.md  agents/openai.yaml
```

落盘规则只有两条：

| 源 | 目标 |
| --- | --- |
| `scripts/`、`repo/` 之外的所有文件 | `.agents/skills/<name>/` 下同名路径 |
| `repo/` 下的文件 | 仓库根目录下的同名路径（mypr 用它把 PR 模板一起带过去） |

所以改名、加 `references/`、拆文件都不用改脚本——脚本 `readdir` 自己，没有第二份路径表可维护。

## 三条要求

1. **逐字一致。** 副本与源必须字节相同；要改规则就改源再 `--apply`。仓库自己的补充写进 `AGENTS.md`，不改 `SKILL.md`。
2. **不用软链。** Windows 需要额外权限、跨盘失效、打包会丢；安装脚本会直接拒绝软链目标。
3. **名字带 `my` 前缀。** `mypr` / `mydocs` / `myshuohua` 而不是 `pr` / `docs` / `shuohua`：`.agents/skills/` 里可能已经有项目自有或全局安装的同名 Skill，重名时生效哪份取决于加载顺序，出了问题也看不出调用的是哪一个。

## 为什么不装到用户目录

`.agents/skills/` 随仓库提交，规则和代码走同一次评审、同一次回滚，换机器、换 Agent、换协作者都不用重新配置。装到用户级目录则会把某个私有仓库的规范带进无关项目，且无法在 PR 里审查。
