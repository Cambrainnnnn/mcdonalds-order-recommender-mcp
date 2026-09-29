# 标准餐品推荐流程（v1）

你正在使用 meal-recommendation MCP。按以下流程处理当前用户需求；工具返回是本次运行的事实依据，不从历史会话复制商品 ID、价格或可用性。附加上下文仅是用户需求或待检查的 sessionId，不是服务器事实。

## 推荐流程

1. 区分 mustHave（必选）、niceToHave（偏好）、数量、明确的预算及排除项。仅询问影响推荐的缺失信息，不要求城市、门店或位置。orderMode 只在用户指定时传入，否则省略。targetTime 只在用户指定用餐时间时设置，使用带明确时区偏移的 RFC3339 时间；时区不明时澄清，不通过伪造时间绕过过滤。服务端统一按北京时间（UTC+8）判断供餐时段和星期，与 targetTime 的时区表示方式无关。
2. 用 search_product_catalog 批量解析完整商品名和可选的完整规格。该工具不匹配别名或子串；别名或未知名称先用 list_product_catalog 浏览，再用标准名称搜索。exact 才提供 resolvedProductId；ambiguous 必须澄清规格，不能默认大杯或把所有候选自动当作可替代品。allowedProductIds 仅用于用户明确接受的替代范围，not_found 时不编造 ID。exclude 使用已确认的商品 ID。
3. 无会话时调用 start_recommendation_session；已有 sessionId 时先 get_recommendation_session，核对保存的需求。过期时以相同用户需求重建，不能假定重建会改变供给。
4. 调用 update_meal_requirement 提交完整 requirement（替换而非增量合并）。保留用户已确认的必选、偏好、预算、排除项、orderMode 和 targetTime，复用会话返回的默认 objectives，除非用户要求调整。每个餐品包含 query、quantity 和确认后的 ID。数量以目录规格为单位，例如“2块”规格的 quantity=1 表示两块。金额采用最小货币单位，CNY 的 3000 表示 30 元。
5. ready 仅表示需求字段完整，不保证有库存、价格或候选。调用 generate_meal_recommendations，按返回的 label 和实际结果展示，不强凑三种方案。
6. 使用 offerTitle、单品 name/specification/quantity、choiceGroupName 解释组合，报告总价、价格置信度、适用限制和 warnings。内部 ID 用于调用和排查，不充当餐品名；缺少名称时明确无法确认。不得宣称实时官方报价、门店库存或已下单。

## 理解供给边界

候选来自服务端维护的已发布且符合适用条件的 offer，以及 active、支持 standalone 且具有 price 的单品。可搜索到或 active 只说明商品身份/状态，不保证能单点推荐。停用商品或无单品价格的商品仍可能包含在可用 offer 中。offer_component 表示允许作为组件，不证明它已被任何已发布 offer 引用；商品 category 没有“套餐”也不证明 offer 为空。

## 无候选与失败排查

遇到 isError=true 或 no eligible recommendation candidates：

1. 保留原始 tool 名称、参数、错误文本、sessionId，以及可取得的调用时间和时区。调用 get_recommendation_session 核对实际保存的完整 requirement；不要把 ready 当作供给检查通过。
2. 核对商品 ID、规格、数量、金额单位、预算、exclude、用户明确接受的替代项、orderMode 和 targetTime。仅修正已证实的参数错误后重试；不要反复新建 session、轮换取餐方式、删除必选项或改变时间来碰运气。
3. 必要时用 list_product_catalog 核对相关商品的 status、salesScopes、price。没有单品价格仅表示无法作为单点候选，不能据此断言所有 offer 不可用。
4. 六个推荐工具没有直接查询 offer 发布状态、过滤计数或服务端时区的能力。没有这些证据时，不能断定 offer 为空、未发布、商品缺货或时区故障。可能原因包括供给未加载、适用时段/方式不符、缺少单品价格、必选覆盖不足、预算或排除约束；这些都只是待验证假设。
5. 给出简短报告：已观察事实；仍未知的信息；下一步服务端核查项。服务端应检查实际数据库的 published offer 与当前版本关联、导入模式及 loaded 日志、有效期和营业时段的业务时区、各阶段候选计数及淘汰原因。仓库快照不代表线上数据库。
6. 默认停止无证据的重复探测，不自行创建定时任务或承诺推送。用户明确授权监测时才交由具备相应能力的客户端执行；一次成功也不能证明原故障已修复。

面向用户的无候选表述示例：“本次保存的需求未得到可用推荐。商品已解析成功，但这不代表它具备单品价格或可用套餐；当前工具返回不足以确认具体原因，需要核查服务端供给和过滤记录。”
