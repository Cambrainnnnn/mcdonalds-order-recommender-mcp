# 套餐截图提取工作区

这里保存截图提取的 Schema、DeepSeek 视觉提示词和格式示例。识别结果是中间数据，不是 `/v1/admin/offers` 的请求体。本目录暂不包含模型调用、自动去重或导入程序。

## 目录

```text
offer-intake/
├── images/                              # 真实截图，已预留；内容默认不提交 Git
│   └── capture-001/                     # 使用时创建，一次采集一个目录
│       ├── 01-detail.png
│       └── 02-rules.png
├── outputs/                             # 真实输出，已预留；内容默认不提交 Git
│   └── capture-001/                     # 使用时创建
│       ├── capture.json                 # 人工/程序维护的采集清单
│       ├── progress.json                # 模型维护的识别进度，断点续跑的唯一依据（不是最终结果）
│       ├── progress.20260927T1140.json  # 批次不一致或人工要求重来时的归档进度，保留旧文件
│       ├── extraction.v1.json           # 模型第一次提取结果
│       └── extraction.v2.json           # 重试或修订结果，保留旧结果
├── schemas/offer-extraction.schema.json # JSON Schema Draft 2020-12
├── prompts/deepseek-vision.md           # 完整可复制提示词，已内嵌同一份 Schema，含进度与续跑协议
└── examples/
    ├── capture.example.json            # 采集清单模板，不属于模型输出 Schema
    ├── progress.example.json           # 进度文件格式示例（提示词 v1.1）
    └── extraction.example.json         # 虚构格式示例，不是真实可购买套餐
```

## 直接使用

1. 在项目根目录执行 `mkdir -p offer-intake/images/capture-001 offer-intake/outputs/capture-001`。
2. 把同一平台商品的详情、选项、使用规则截图放进 `images/capture-001/`。尽量不要把不同商品混在一次调用里；长截图可切成有重叠区域的多张图，文字应清楚。
3. 将 `examples/capture.example.json` 复制到 `outputs/capture-001/capture.json`。填写平台、链接、带时区的采集时间，按实际文件补充 images 列表。路径相对于 `offer-intake/`。未知值保留 null；不要让模型猜测哈希或采集时间。保存实际使用的模型名。
4. 在支持图片输入的 DeepSeek 界面或调用工具中上传截图，复制 [完整提示词](prompts/deepseek-vision.md) 的全部内容。它已包含 Schema，不必再手动拼接。可以附上采集清单，明确每张图片与 imageId 的对应关系；没有清单时按上传顺序编号 img_001、img_002……，captureId=null。
5. 按提示词“进度 1”判断执行模式：
   - 文件模式：运行环境提供文件工具，且模型能看到本目录。提示词要求模型自己在 `outputs/<captureId>/progress.json` 每识别一张图片落盘一次，收尾时自己写 `outputs/<captureId>/extraction.v<N>.json`。人工只做校验，不需要搬运 JSON。
   - 纯文本模式：没有文件工具时，模型只能返回文本。此时把返回的纯 JSON 手工保存为 `outputs/capture-001/extraction.v1.json`；仅把本地文件路径写进 prompt 不会发送图片。检查响应没有被输出长度上限截断；截断结果不能当作完整数据。
6. 按 Schema 校验，再检查 issues、证据与业务规则。JSON 可解析或 Schema 通过都不代表内容已正确，不直接自动发布。

## 识别进度与断点续跑

提示词 v1.1 引入了进度文件 `outputs/<captureId>/progress.json`（无清单时用 `outputs/manual/progress.json`）。它是模型自己的工作状态：已处理到哪张图片、每张图片关联哪些套餐、累积的 evidence 与字段、待人工确认的问题、修订记录。它不属于 `schemas/offer-extraction.schema.json`，也不是可发布数据，格式见 [progress.example.json](examples/progress.example.json)。

- 落盘时机：每张图片识别完成后立即更新并原子写入（先写 `progress.json.tmp`，确认可解析后替换）；全部图片完成后写最终结果文件，再把 `status` 置 `completed`。
- 断点续跑：中断后重新提交同一批图片和同一份采集清单，说明“继续”。模型先读 `progress.json`，跳过 `state` 为 `done` 的图片，沿用已有 `localId` 与新证据合并，不重新编号、不重排数组（evidence/issue 的 JSON Pointer 依赖数组下标）。
- 批次不一致：`captureId`、图片数量或 imageId 列表与进度文件不符时，模型不得静默合并，会把 `status` 置 `blocked`、记录差异，并把旧进度归档为 `progress.<时间戳>.json` 后新建，等人工确认。
- 纯文本模式：没有文件工具时无法自动落盘。每次调用只返回一段结果，未看完的部分用 `completeness="partial"` 和 `issues`（`code="incomplete"`）如实标记；续跑要把上一次的完整输出贴回并说明继续，由模型合并。
- 人工注意：`progress.json` 只是中间状态，不能当结果发布；重跑不要删除旧的 `progress*.json` 和 `extraction.v*.json`，便于比对。

如采用 API，可把提示词作为文字指令，图片按所用端点的图片内容块传入；进度协议要求调用方提供文件读写工具（例如本地 agent 直接操作本目录），纯 API 文本调用只能走降级模式。

截至 2026-09-27 查阅的 DeepSeek 官方文档：`deepseek-flash` 支持图像输入；旧名 `deepseek-v4-flash-vision-exp` 已由最新 Flash 承接。Chat Completions 可用 `response_format={"type":"json_object"}`；这只保证 JSON 模式，仍需本地按本目录 Schema 校验。Responses API 文档提供 JSON Schema 输出格式，采用该端点时可将 Schema 作为结构化输出参数传入；具体关键字支持以服务响应为准。第三方网关的模型名称和参数支持需按网关确认。

- [DeepSeek 图像理解](https://api-docs.deepseek.com/zh-cn/guides/vision/)
- [Chat Completions API](https://api-docs.deepseek.com/zh-cn/api/create-chat-completion/)
- [Responses API](https://api-docs.deepseek.com/zh-cn/api/create-response/)

## 校验

可以使用任意支持 Draft 2020-12 的 JSON Schema 校验器。例如在项目根目录运行以下命令（Python 需有 `jsonschema` 包）：

```bash
python3 - <<'PY'
import json
from pathlib import Path
from jsonschema import Draft202012Validator

root = Path('offer-intake')
schema = json.loads((root / 'schemas/offer-extraction.schema.json').read_text())
data = json.loads((root / 'outputs/capture-001/extraction.v1.json').read_text())
Draft202012Validator.check_schema(schema)
Draft202012Validator(schema).validate(data)
print('Schema 校验通过；还需复核引用、事实及业务条件。')
PY
```

Schema 之外还需检查：

- 所有 imageId 在清单及实际输入中存在；套餐局部 ID 唯一；所有选择组引用存在。
- evidence/issue 的 JSON Pointer 可定位，证据确实支持所指字段。
- 已知 minSelect ≤ maxSelect，选择数量、选项数量和重复规则能够构成合法组合。
- fixed 不得带 choiceGroupId；choice 必须引用选择组；unknown 不能自动当作固定项。
- 缺失价格、名称、规格、数量、选项、必要加价规则，或存在冲突/无法表达的限制时，不进入自动导入。
- null 表示未知，0 表示明确为零，false 表示明确否定；[] 的含义须结合 completeness。不得在转换时直接套用服务端默认值抹掉未知状态。
- 只校验 `extraction.v*.json`。`progress.json` 是模型的工作状态，字段会随进度增删，不参与 Schema 校验，也不要转换或发布。

## 与现有导入 API 的衔接

1. 保留原始结果，根据目录及已确认别名将 nameRaw/specificationRaw 解析为标准商品。规格缺失不自动补“标准份”，模糊匹配不直接合并。
2. 使用可信的采集清单提供平台/品牌/链接，确认提取价格属于可用售价；券后价、起价、会员新客条件不能静默丢弃。
3. 固定组成和选择组转换为现有 components/choiceGroups，复用标准单品 ID，或以 productBindings 补建明确的新单品。
4. group_1 等仅为提取时的局部键。当前 API 要求选择组 ID 为正数十进制雪花字符串，转换时须重新分配并同步替换引用；数据库商品 ID 也不能由视觉模型编造。
5. 日期原文经确认后再转换。当前 API 未完整支持的限制保留在待处理记录，不能丢掉限制后发布。
6. 保存最终请求载荷及稳定的 Idempotency-Key，按套餐独立提交草稿；重试原样重发，修改载荷或重新处理使用新的修订键。

细节见 [现有截图套餐录入 API](../docs/offer-ingestion-api.md)。本目录提取 Schema 使用的 selectionKind、evidence、issues 等字段不属于该 API，不能原样提交。

## 维护约定

Schema 变更时同步更新版本、示例和提示词末尾内嵌的 Schema。采集清单记录模型/提示词/Schema 版本；输出修订保留原件，以便重跑和比对。真实截图、清单和识别结果默认由本目录 `.gitignore` 忽略；格式示例随 Git 管理。

当前版本：提示词 v1.1（含进度落盘与断点续跑协议）、输出 Schema v1.0、进度文件 progressVersion v1.0。改动进度协议时，必须同步更新提示词“进度 1”至“进度 6”、`examples/progress.example.json` 和本节版本号；提示词末尾内嵌 Schema 必须与 `schemas/offer-extraction.schema.json` 完全一致。
