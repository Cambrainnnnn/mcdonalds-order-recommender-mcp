# MCP 标准推荐提示词

服务提供 6 个 tools 和 1 个 prompt：`recommend_meal`。stdio 和独立 HTTP MCP 使用同一个注册入口。

Prompts 和 Resources 是 MCP 的不同能力。客户端通过 `prompts/list` 发现模板，再通过 `prompts/get` 获取消息并纳入模型上下文；模板不会因为连接成功而自动执行。服务也在初始化的 `instructions` 中给出简短使用边界，关键 tool 描述保留独立可读的约束。不支持 prompts 的客户端仍可使用原有六个工具。

协议参考：https://modelcontextprotocol.io/specification/2025-11-25/server/prompts

## 获取与使用

模板参数均可省略：`request` 为用户原始用餐需求，`session_id` 为继续推荐或排查失败的既有会话 ID。无参数时使用当前对话。获取模板本身不会创建会话、查询数据库或生成推荐。

```json
{"jsonrpc":"2.0","id":1,"method":"prompts/list"}
```

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "prompts/get",
  "params": {
    "name": "recommend_meal",
    "arguments": {
      "request": "必选麦辣鸡腿汉堡和麦辣鸡翅，最好还有可乐和圆筒冰淇淋"
    }
  }
}
```

排查已有失败会话时传入 `session_id`；会话已过期或不存在时不能假定还能读回需求，应使用保留的调用记录。客户端应将返回的 `messages` 作为提示上下文使用，而不是把 `prompts/get` 当成推荐结果。

## 维护与诊断边界

正文唯一来源为 `internal/adapters/inbound/mcpserver/recommendation_prompt.md`，通过 `go:embed` 编译进服务；修改后需重新构建部署。`prompts.go` 注册模板、参数和简短初始化说明。

模板覆盖名称/规格解析、用户确认替代品、完整替换需求、金额和数量单位、结果解释，以及无候选时保留证据和停止盲目重试。它要求明确区分工具观察、待验证假设和服务端检查项，不把商品 active、session ready、缺少套餐类目或 offer_component 当作供给存在与否的证明。

模板不修复时区过滤、导入、缺价或候选生成缺陷，也不能替代结构化诊断。后续服务端诊断应提供请求关联标识、有效业务时间/时区、读取及过滤后的 offer 数、可单卖且有价的商品数、生成候选数和硬约束淘汰原因。客户端不能自行填写这些尚未返回的指标。

验证：`go test ./internal/adapters/inbound/mcpserver`，覆盖初始化能力、发现、无参数获取、上下文参数保真、未知模板/参数拒绝，以及原有工具调用回归。
