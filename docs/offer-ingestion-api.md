# 截图套餐录入

Agent 负责识别截图、确认商品名称/规格与优惠规则；服务负责精确复用单品、
事务补建、规则校验、防重和保存草稿。当前没有服务端 OCR，也不自动发布。
MCP 继续只提供目录查询与推荐；保存使用 HTTP 运营 API。

## 工作流程

1. 从截图提取渠道、标题、基础价、有效期、固定项、选择项、规格及加价。
   无法确定的名称、规格、重复规则或加价单位须向用户澄清，不按 OCR 猜测合并。
2. 用 `search_product_catalog` 或 `POST /v1/products/search` 搜索标准单品。
   唯一确认项使用返回的 `canonicalProductId`；歧义候选须确认，不能任选一个。
3. 对未匹配的明确单品，提供 `productBindings`。服务按规范化品牌、名称、规格
   精确复用或补建；别名、分类、OCR 相似度不参与身份判断。
4. 为一次逻辑写入生成唯一 `Idempotency-Key`，将请求 JSON 保存到本地以便原样重试。
   请求成功后检查返回的草稿、单品映射、规则与金额。修改内容须换新键。
5. 草稿确认后按已有业务流程调用 review、publish。截图保存不自动执行这些操作。

## 创建与修改

继续使用 `POST /v1/admin/offers`、`PUT /v1/admin/offers/{offerId}/draft`。
提供 `productBindings` 或 `Idempotency-Key` 时返回聚合结果：

```json
{
  "offer": {"id":"服务器生成的ID", "status":"draft"},
  "products": [{"componentIndex":0,"productId":"服务器生成的单品ID","created":true}]
}
```

`offer` 是完整套餐；`products` 逐项对应请求中的绑定，不含直接引用 ID 的组件。
首次创建返回 201，修改返回 200；幂等重放保持原状态码和响应内容。
不提供绑定与幂等键的旧请求继续返回原来的套餐对象。

示例：两个薯条规格与两种汉堡，共 4 个选项中选 2 次。可重复选择，
中薯最多 1 次，同一组内中薯和大薯不能同时出现。

```bash
curl -X POST http://localhost:8080/v1/admin/offers \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: screenshot-20260913-001' \
  --data-binary @data/examples/offer-ingestion.json
```

选择组 ID 必须为正数十进制雪花字符串。示例 ID 仅用于示例分组；实际请求可使用
调用方分配的唯一数值组 ID。商品和套餐 ID 由服务端生成，不能根据名称编造。

`productBindings[].componentIndex` 是 `components` 的零起始下标，每个下标最多一次。
绑定项必须省略或留空 `canonicalProductId`；同一组件不能同时提供 ID 与资料。
单品资料支持 `brand`、`name`、`specification`（必填），以及 `category`、`aliases`。
缺失单品默认启用、无参考单价，仅支持 `offer_component`，避免错误参与单卖推荐。
已存在单品保留所有资料、状态、价格与销售范围；停用单品可以复用，
但缺少 `offer_component` 范围时整笔写入失败，不自动修改其销售范围。

## 选择与加价

| 字段 | 规则 |
| --- | --- |
| `choiceGroups[].minSelect/maxSelect` | 相同表示精确 N 选；4选2、5选2均设为 2 |
| `choiceGroups[].allowRepeat` | 默认 false；true 时可以重复选同一个选项 |
| `components[].maxRepeat` | 每个选项最多选择次数；0/省略为组允许上限，不可重复组最多 1 次 |
| `components[].exclusiveKey` | 同一组内同键的不同选项互斥，用于不同规格；同一个选项仍可按重复规则重复 |
| `components[].quantity` | 每次选择获得的单品份数，不是选择次数 |
| `components[].extraPrice` | 非负最小货币单位加价 |
| `components[].extraPriceMode` | 默认 `per_selection`，每次选择加一次；`per_unit` 按 quantity 乘算 |

固定项也支持按次/按份加价；重复上限与规格互斥只用于分组项。
同一组内不能重复录入相同单品 ID 来绕过不可重复限制。
同一商品在不同组中可以分别选择。`exclusiveKey` 不跨组生效。
最多 100 个组件、100 个组、每组最多选择 100 次、固定项与所有组选满合计最多 100 次、最多 10,000 个合法组合；
复杂搜索另有 1,000,000 步上限。数量或金额溢出、无合法组合均拒绝保存。
保存和推荐展开复用同一领域实现。

没有 `choiceGroups` 的历史快照继续把组件作为固定项，历史分组项的加价保持不收取。
默认加价方式不变；数据库迁移不改写已有发布快照。

## 防重与幂等

可提供渠道商品编号 `externalId`：相同规范化渠道 + 相同编号为同一套餐，
即使价格变化也不能创建第二条，应修改原草稿或按原流程下架、修改。
没有编号时使用内容指纹，包含渠道、标题、价格、有效期、适用条件、商品组成与选择规则。
组件/组数组顺序、分组 ID/名称、购买链接和运营时间戳不影响指纹。
新增重复和把草稿改成另一套餐的身份都返回 409 `offer_conflict`。
历史重复行保留，迁移为最早记录登记唯一身份；新写入也会检查未登记的历史行。

`Idempotency-Key` 可选，最多 200 字节，不允许首尾空白或控制换行，
一个请求只能提供一个非空键。键在全部创建/修改操作中共用命名空间。
相同键与相同解析后的请求内容返回原始结果；键相同而载荷或目标 ID 不同返回
409 `idempotency_conflict`。载荷数组顺序的变化也属于不同请求。
发布或后续修改不改变已记录的重放结果；失败不占用键。
不同键提交相同套餐返回业务冲突，不默默覆盖或自动发布。

其他错误延用 400 `invalid_offer`、404 `offer_not_found`、409 `invalid_offer_transition`。
请求 JSON 拒绝未知字段。CSV 保留原格式，也应用选择规则与套餐防重，
但不支持聚合补建或请求级幂等；这些功能应使用 JSON 接口。

内存模式使用两个仓储的固定顺序锁和副本提交，进程退出会丢失数据。
PostgreSQL 使用数据库事务、固定表锁顺序及唯一索引，幂等结果持久化。
事务失败不会遗留新单品、套餐或幂等记录。当前写事务串行化，目录读取不受表锁阻塞；
适合当前人工录入规模，未实现高吞吐写入或幂等记录清理策略。
