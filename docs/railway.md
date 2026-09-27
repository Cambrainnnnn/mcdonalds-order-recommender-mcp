# Railway 部署远程 MCP

生产只需要两个服务：本仓库的 `cmd/mcp-http` 和 Railway PostgreSQL。Hermes 是可选测试 agent，不参与生产部署。原有 `cmd/api`、`cmd/mcp` 和 Dockerfile 继续支持 REST / stdio 测试。

## 部署

1. 在 Railway 项目中新增 PostgreSQL 服务（下文假定名称为 `Postgres`）。
2. 从本仓库创建应用服务，仓库根目录作为构建目录。根目录 `railway.json` 指定 `Dockerfile.mcp-http`、单实例和 `/healthz` 部署检查。无需 Compose、额外启动命令或 Hermes。
3. 在应用服务 Variables 中设置：

   | 变量 | 值 |
   | --- | --- |
   | `DATABASE_URL` | `${{Postgres.DATABASE_URL}}`（按实际数据库服务名调整） |
   | `APP_MCP_TOKEN` | 本地运行 `openssl rand -hex 32` 生成的随机密钥 |
   | `APP_EXTERNAL_TIMEOUT` | `60s`（首次数据库连接和 migration 的总超时） |

4. 部署应用，在 Networking 中 Generate Domain。Railway 提供 HTTPS，应用自动监听 `PORT`。不要配置只监听 localhost 的 `APP_HTTP_ADDRESS`。
5. 检查 `https://<你的域名>/healthz` 返回 200。日志应包含 PostgreSQL 初始化成功与 MCP HTTP 启动信息。

应用启动时自动执行数据库 migration。缺少数据库 URL、缺少 Token 或数据库初始化失败都会终止启动，避免生产意外使用内存目录。`APP_DATABASE_URL` 优先于 `DATABASE_URL`；`APP_HTTP_ADDRESS` 优先于 `PORT`，最后默认 `:8080`。`APP_MCP_TOKEN` 与历史的 `APP_MCD_MCP_TOKEN` 是不同配置。

数据库保存商品和套餐；新数据库只有表结构，没有自动导入业务数据。若需迁移已有目录，应先备份，再将数据导入 Railway PostgreSQL。运营维护可在本机用 `cmd/api` 连接数据库的公网连接地址（仅监听 `127.0.0.1`）；生产 MCP 镜像不提供运营写接口。不要将原有无鉴权 REST API 暴露到公网。

推荐会话仍在应用内存中。保持单实例，关闭 Railway Serverless 自动休眠；重启或重新部署后客户端需要重新调用 `start_recommendation_session`。`/healthz` 是启动后的存活检查，并非持续数据库可用性检查。

## 本地客户端连接

连接类型选择 **Streamable HTTP**，URL 为 `https://<你的域名>/mcp`，在每次请求附带 `Authorization: Bearer <APP_MCP_TOKEN>`。客户端无需本地启动 server、Hermes 或连接 PostgreSQL。

支持 `url` / `headers` 格式的 MCP 客户端可以使用以下模板（不同客户端的字段可能不同）：

```json
{
  "mcpServers": {
    "meal-recommendation": {
      "url": "https://<你的域名>/mcp",
      "headers": {
        "Authorization": "Bearer <你的APP_MCP_TOKEN>"
      }
    }
  }
}
```

Token 属于密钥，使用客户端密钥配置机制保存，不要提交到 Git。这是预共享 Bearer Token 认证，客户端需要支持自定义请求头；不提供 OAuth 登录。当前禁止带 `Origin` 的浏览器请求，面向本地桌面/命令行客户端。传输无状态，不需要保存 `Mcp-Session-Id`；工具返回的业务 `sessionId` 仍需保留供后续调用。

可以先在本机验证握手，`MCP_URL` 指向远程 HTTPS 地址：

```bash
export MCP_URL='https://<你的域名>/mcp'
read -rs 'APP_MCP_TOKEN?MCP Token: '; echo
export APP_MCP_TOKEN

# 应返回 401；healthz 不需要 Token
curl -i "$MCP_URL"

# 应返回协议版本、serverInfo 和 capabilities
curl --fail-with-body "$MCP_URL" \
  -H "Authorization: Bearer $APP_MCP_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"local-smoke-test","version":"1.0"}}}'

# 应返回六个目录查询/推荐工具
curl --fail-with-body "$MCP_URL" \
  -H "Authorization: Bearer $APP_MCP_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-03-26' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

上述密钥输入命令适用于 macOS 默认 zsh。轮换 Token 时更新 Railway 变量并重新部署，同时更新本地客户端。

## 本地复现生产镜像

```bash
export APP_MCP_TOKEN="$(openssl rand -hex 32)"
docker compose -f compose.yaml -f compose.mcp-http.yaml up -d --build
export MCP_URL='http://localhost:8080/mcp'
# 使用上面的 curl 验证；同一终端保留 APP_MCP_TOKEN。
docker compose -f compose.yaml -f compose.mcp-http.yaml down
```

参考：[Railway 配置文件](https://docs.railway.com/config-as-code/reference)、[PostgreSQL](https://docs.railway.com/databases/postgresql)、[部署健康检查](https://docs.railway.com/deployments/healthchecks)。
