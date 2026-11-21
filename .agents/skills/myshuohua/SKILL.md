---
name: myshuohua
description: "Use for Chinese prose in GitHub repository documentation: README, docs/, CHANGELOG, PR descriptions, release notes, and maintenance guides. Enforce restrained technical writing: remove casual filler, truncated terminology, vague referents, incorrect verb-object pairs, marketing claims, translationese, and redundant explanations; run the bundled wording linter and review meaning manually. Do not decide document placement (mydocs), change code identifiers, or treat informal issue conversations as formal documentation."
---

# GitHub 仓库文档：准确、完整、克制的中文

这套规则针对仓库里长期维护的正式技术文字，不把聊天口气当成「自然」，也不靠堆术语显得专业。语言必须明确说明**对象、动作、条件、结果**；没有信息的衔接词、口语填充、营销承诺和工程黑话一律优先删除。

检查顺序：**先核对技术事实和术语 → 跑 lint → 逐句修正搭配与省略 → 精简重复表达 → 对照原文确认没有丢事实**。lint 只能定位线索，不能替代上下文判断；不能为了通过阈值改变产品行为或安全边界。

## 不适用的情况

- 只改错别字、失效链接、过期命令。直接改。
- 已经准确、清楚、符合正式文档语域的句子，不为了改写而改写；对方只要求找问题时，直接给出问题和最小修改。
- 内容还在错的文件里。先判断该放哪，改句子放在后面。

## 安装与更新

这个 Skill 自带安装脚本。源在 `.github/engineering/skills/myshuohua/`，Agent 读的是 `.agents/skills/myshuohua/`：

```sh
node .github/engineering/skills/myshuohua/scripts/install.mjs --check .
node .github/engineering/skills/myshuohua/scripts/install.mjs --apply .
```

`--check` 列出不一致的文件并以非零状态退出；`--apply` 写入，遇到有本地改动的路径直接拒绝覆盖。`--source` 打印这份副本来自哪个提交和日期。

装好之后，lint 在 `.agents/skills/myshuohua/lint-words.mjs`，规则在同目录的 `rules.json`。不要改装好的副本，改源再 `--apply`。

## 第一步：跑 lint

```sh
node .agents/skills/myshuohua/lint-words.mjs README.md README.en.md docs
```

五条规则：

| 规则 | 管什么 |
| --- | --- |
| `semicolon-density` | 每千字的分号数。分号是硬粘两句话的胶水 |
| `sentence-length` | 单句上限 100 字，整篇 p90 上限 70 |
| `bullet-sentences` | 一条 bullet 最多两句 |
| `term-casing` | 同一个词在全篇只用一个写法 |
| `banned-phrases` | 营销词、论文腔、翻译腔、伪口语、空话，外加几个句式正则 |

`--rules` 看阈值和理由；高置信度的营销词、伪口语和工程黑话标为 `error`，默认就会退出 1；`--strict` 还会让 warning 退出 1；`--json` 给 CI 用。现有句长与分号阈值曾用 ligule、tasklutein、muxshift、DiggerTerm 四个仓的 README 校准；扩充词表时仍需检查这些真实样本的误报，不能把命中数当作质量指标。

英文文档也可以传入：句长只量中文句子，固定中文短语只在实际命中时报告。对代码块、行内代码和界面原文采用保留原则，必要时用单条规则的 `lint-ignore-next-line` 注释。

文档里要引用坏例子时，用 `<!-- lint-ignore-next-line banned-phrases -->` 关掉那一行，`<!-- lint-ignore-file banned-phrases -->` 关掉整个文件。不要为了过 lint 把例子改掉。

## 第二步：逐词核对

lint 报告的命中项必须处理或说明保留理由；没报告的句子也要逐个检查。常见问题：

1. **单字动词**。「取不到播放列表」「点一下按钮」「只改落位」。模型为了短把双音节动词压成一个字，压完每个字都认识，整句不是人话。
2. **词被砍掉一半**。「跨流对轴」「点一下切源」「首样本时间」。省掉的是限定词，剩下的字凑不成术语，只有项目里的人看得懂。
3. **动词接错宾语**。「承载对齐能力」「打通链路」「兜住超时」「沉淀规则」。写清楚哪个模块读取、校验、传递、重试或保存了什么，不能以抽象动词代替技术行为。
4. **无意义口语**。「这块」「那边」「搞定」「跑通」「先查一下」「就行」。仓库文档不是即时聊天；用明确的组件、命令、操作与结果替换，不用另一句口语解释口语。
5. **含糊指代与空泛动作**。「做相关处理」「提供丰富能力」「这边会统一收口」。缺少对象、条件或可核对的结果时，必须查证后补齐，无法查证就删除断言。

判断法：把动词换成「做」再读一遍，「做跨源对齐的能力」不通，说明「承载」是硬撑上去的。宾语那一路问「什么的原因」「谁的地址」，答不上来就是缺限定。

另有一类不是词错，是词替事实撑场面：「强大的」「完善的」「丝滑」「一站式」。删掉以后读者没损失，说明本来就不该写。给不出数字就把条件写清，「只在 1080p 下测过」比「性能良好」有用。

## 第三步：句子与段落

- **说清主体**：说明由哪个组件执行动作；技术文档允许「请求」「服务」「处理器」作主语，前提是动作和职责准确，不要机械改成人称句。
- 动词别变名词。「进行校验」「提供支持」「实现隔离」，删掉「进行」「提供」「实现」，名词直接当动词用。
- 先说会发生什么，再说前提。条件是校验顺序，不是读者要的阅读顺序。
- 同一约束中不要把「会」「可能」「请」「需要」「应该」叠加成含糊语气。分别说明已知行为、可能性和必须执行的步骤；硬性要求用「必须」「不能」，不把未证实的保证写成确定结论。
- 分号只用于必要的并列关系。多个独立事实优先分句；不能为了拆短句丢掉前置条件或适用范围。
- **逐句消除模糊**：遇到「相关」「部分」「一些」「支持了」「做处理」，检查是否缺少具体对象、触发条件或结果；不清楚时回查代码、接口和测试，不猜。
- **整段去重**：同一个事实不要在标题、引言、三条 bullet 和结尾重复四遍；保留最利于检索的一处，其余删除或改为链接。

## 不该做的

- 别以「反口语」为由删掉用户必须知道的失败行为、默认值、兼容限制或数据安全保证。简洁性不能盖过准确性。
- 别把技术名词换成大白话。「句柄」「事务」「回滚」是准确的，换成「把手」「一批操作」「取消」反而错。
- 别为了显得专业加术语密度。技术感来自数字、条件、命令和边界，论文腔是另一种机器味。
- 别改代码标识符、命令、字段名、界面原文。它们保持原样，外面那层中文才对它负责。
- 别把长句一律拆成短句。三五个短句连排读着像幻灯片，该连的地方还得连。

## 文档类型的用语要求

| 文档 | 重点检查 |
| --- | --- |
| `README` | 主要功能、前置条件和限制说具体；不要用「一站式」「轻松」「开箱即用」替代操作步骤 |
| `ARCHITECTURE` / `API` | 组件、接口、数据与状态的名称准确；写明触发条件和结果，不用「打通链路」「兜住异常」 |
| `CHANGELOG` / Release | 只写已经交付且可核对的变化；「优化体验」「完善能力」不能代替行为差异 |
| PR 描述 | 讲清最终实现和真实验证；不用聊天口气复述开发过程 |
| `AGENTS.md` / 维护手册 | 按现有英文约定写 AGENTS；维护步骤使用可执行的命令、条件和判定标准 |

## 改完怎么验

1. 每句能否指出具体对象、动作、条件或结果；必要的限制是否保留。
2. 是否出现无意义的聊天填充、营销词、论文腔、翻译腔、缺失限定词或错误动宾搭配；逐项修正，不将命中项无条件替换成同一个词。
3. 对照改写前后的功能、命令、参数、错误语义、兼容范围与安全约束；如有变化必须有事实依据。
4. 回读整段，删除重复结论和没有信息的过渡；保留必要的逻辑衔接与有实际意义的比较。

## 参考

- [references/language-zh.md](references/language-zh.md)：常见用词和句式问题，包含模糊指代、空泛动作和信息保真对照。
- [references/bad-good.md](references/bad-good.md)：整段对照，取自真实 README，后面写清为什么错。
