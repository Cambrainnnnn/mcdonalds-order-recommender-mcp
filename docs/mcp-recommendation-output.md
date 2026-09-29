# MCP 推荐结果中的展示名称

`generate_meal_recommendations` 在 `result.recommendations[].candidate.purchaseLegs[]` 返回套餐和单品的展示信息。stdio 和 HTTP MCP 使用相同输出；REST 推荐接口也共享这些字段。

| 字段 | 来源与用途 |
| --- | --- |
| `offerTitle` | 已发布套餐版本的标题；单点组合不设置套餐标题 |
| `offerVersionId` | 已发布套餐版本 ID，供系统识别 |
| `products[].canonicalProductId` | 标准商品 ID，供工具调用及规则匹配 |
| `products[].name` | 商品目录中的标准名称 |
| `products[].specification` | 对应商品 ID 的具体规格，例如小杯、大杯 |
| `products[].brand` | 商品目录中的品牌 |
| `products[].quantity` | 实际推荐数量 |
| `products[].choiceGroupName` | 已发布套餐中对应选择组的名称；无名称时省略 |
| `products[].choiceGroupId` / `extraPrice` | 实际选择组标识与加价，保持原有语义 |

agent 应使用名称、规格和数量向用户描述实际推荐内容。内部 ID 用于后续工具调用，不应当作餐品名称展示；同名单品必须结合规格区分。`unwantedProductIds` 可与本次推荐 `products` 中的 ID 对照得到名称。

套餐中的单品即使已停用或仅允许作为套餐组件，也会从完整商品目录获取名称。名称来自本次读取的目录，并非历史商品名称快照；套餐标题和选择组名称来自已发布套餐版本。

历史数据如引用不存在的商品，不猜测名称或把 ID 填入名称字段：保留原有 ID/数量，附加无法确认名称与规格的警告。展示字段为空时省略，agent 不应编造。保留的官方供给路径只返回其真实提供的名称，不推断品牌和规格。

这些字段仅用于解释推荐，不提供购买链接或渠道跳转功能。新增字段不参与候选 ID、价格计算或排序。

验证覆盖真实应用服务和推荐引擎经 MCP 返回的 JSON，包括同名不同规格、套餐内停用单品、选择组加价、单点补充及纯单点推荐。
