# Meal Recommendation Service

当前推荐只使用服务端维护的单品、参考价和已发布套餐。城市、门店、库存和备餐不在当前范围，作为未来可选扩展保留。无需提供城市、门店或取餐方式即可推荐；取餐方式仅在用户指定时作为套餐筛选条件。

单卖候选必须启用、允许 standalone 且有维护价格；停用单品仍可通过已发布套餐参与推荐。报价来自维护数据，不是官方实时报价。现有套餐有效期和供餐时段仍参与筛选。

历史官方 MCP Adapter 保留供未来扩展，但当前 API/MCP 启动装配不启用它，即使设置 APP_MCD_MCP_TOKEN 也不会发起门店、菜单或算价请求。

## 架构

```text
cmd/api
  -> adapters/inbound/httpapi
      -> application/recommendation
          -> domain/recommendation
          <- application ports
              <- adapters/outbound/*
```

- `internal/domain`：纯领域类型和确定性业务规则，不依赖 HTTP、数据库、MCP 或 LLM SDK。
- `internal/application`：用例编排及由应用层拥有的 Port 接口。
- `internal/adapters/inbound`：HTTP 等入站协议适配。
- `internal/adapters/outbound`：提供内存仓储、PostgreSQL 套餐仓储和麦当劳 Streamable HTTP MCP Client。
- `internal/infrastructure`：配置、ID 等技术设施。
- `cmd/api`：唯一的依赖装配入口。

接口定义在使用方所在的应用层。替换 PostgreSQL、LLM 或 MCP SDK 时，只需增加 Adapter，不修改领域层。

## 当前仓库代码结构

```text
.
├── cmd/
│   └── api/
│       └── main.go
├── internal/
│   ├── adapters/
│   │   ├── inbound/
│   │   │   └── httpapi/
│   │   │       ├── handler.go
│   │   │       ├── handler_test.go
│   │   │       └── middleware.go
│   │   └── outbound/
│   │       └── memory/
│   │           └── session_repository.go
│   ├── application/
│   │   └── recommendation/
│   │       ├── ports.go
│   │       ├── service.go
│   │       └── service_test.go
│   ├── domain/
│   │   └── recommendation/
│   │       ├── candidate.go
│   │       ├── session.go
│   │       └── session_test.go
│   └── infrastructure/
│       ├── config/
│       │   └── config.go
│       └── ids/
│           └── random.go
├── docs/
│   └── mvp-architecture-design.md
├── .gitignore
├── go.mod
├── Makefile
└── README.md
```

### `cmd/api`

服务的可执行程序入口，也是当前唯一的依赖装配位置。

- 加载运行配置。
- 创建内存仓储和应用服务。
- 将应用服务注入 HTTP Adapter。
- 启动 HTTP Server，并处理 `SIGINT`、`SIGTERM` 和优雅关闭。

`main.go` 只负责组装对象，不承载推荐规则或业务流程。

### `internal/domain/recommendation`

不依赖数据库、HTTP、LLM 或 MCP SDK 的纯领域层。

- `session.go`：定义推荐会话和结构化用餐需求，提供默认值、校验、完整性判断和会话状态更新。
- `candidate.go` / `engine.go`：定义候选组合模型，并实现有界候选生成、硬约束过滤、可解释评分、Pareto 去重和三类结果选择。
- `session_test.go`：验证领域默认值、会话生命周期和缺失字段判断。

该层的推荐计算保持确定性和无外部 I/O。

### `internal/application/recommendation`

应用用例层，负责业务流程编排和事务边界。

- `service.go` / `recommendations.go`：实现会话创建、读取、需求更新、过期处理，以及套餐加载、官方算价、候选排序的应用编排。
- `ports.go`：定义应用层所需的出站接口及接口 DTO，包括会话仓储、需求抽取、人工套餐、官方菜单、官方算价和候选引擎。
- `service_test.go`：通过可控时钟、固定 ID 和内存仓储测试应用流程。

Port 由使用方——应用层定义。PostgreSQL、LLM 或 MCP Adapter 需要适配这些接口，而不是让应用层依赖具体 SDK 类型。

### `internal/adapters/inbound/httpapi`

HTTP 入站适配层，将 HTTP 请求转换为应用用例参数，并将应用结果转换为 JSON 响应。

- `handler.go`：实现健康检查、会话创建/读取、结构化需求更新和推荐生成 API。
- `middleware.go`：请求日志和 panic 恢复。
- `handler_test.go`：使用 `httptest` 验证 HTTP 状态码和响应结构，不需要监听真实端口。

该层只依赖应用用例接口，不直接访问仓储或外部 MCP。

### `internal/adapters/outbound/memory`

当前开发阶段的出站实现。

- `session_repository.go`：线程安全的内存会话仓储，实现应用层的 `SessionRepository` Port，并在读写边界复制领域对象，避免调用方意外修改仓储内部状态。

该实现会在进程退出后丢失数据。接入 PostgreSQL 时新增并注入 PostgreSQL Adapter 即可，不需要修改领域层和 HTTP 层。

### `internal/infrastructure`

与具体业务无关的基础设施代码。

- `config/config.go`：读取并校验监听地址、会话有效期和关闭超时等环境变量。
- `ids/random.go`：使用加密安全随机数生成会话 ID，实现应用层的 `IDGenerator` Port。

### 根目录文件

- `docs/mvp-architecture-design.md`：完整的 MVP 架构、数据模型和推荐算法设计文档。
- `go.mod`：Go 模块定义，使用 pgx `database/sql` 驱动连接 PostgreSQL。
- `Makefile`：提供 `run`、`test`、`vet`、`fmt` 和 `check` 命令。
- `.gitignore`：忽略本地环境文件、构建产物、覆盖率结果和日志。

### 当前实现范围

已经实现：

- 应用启动、配置加载和优雅关闭。
- 健康检查。
- 推荐会话创建、读取、结构化需求更新和过期判断。
- 结构化需求及候选输出领域模型。
- 第三方单套餐、第三方+官方补购、官方单品组合的有界候选生成。
- 必选数量、排除项、预算、资格和货币等硬约束过滤。
- 偏好、浪费、便利性、价格新鲜度和资格风险评分，以及 Pareto 去重。
- “最低实付”“最符合口味”“性价比最高”三类不重复结果。
- 内存会话仓储。
- PostgreSQL 人工套餐仓储、自动 migration 和不可变发布版本。
- 套餐创建、CSV 导入、复核、发布、再核验和下架 API。
- 麦当劳 Streamable HTTP MCP Adapter 的历史实现（当前推荐不启用）。
- 菜单、报价和人工套餐缓存，以及陈旧缓存降级。
- LLM、人工套餐、官方菜单、官方算价和候选引擎的可替换接口。
- 领域、应用和 HTTP 层测试。

尚未实现：

- 自然语言用户消息处理与需求 Patch。
- LLM Requirement Extractor。
- 推荐快照持久化。

详细的外部依赖和后续增强项见 `todo.md`。

## 运行

要求 Go 1.25 或更高版本。

### Docker Compose（推荐用于测试）

同时启动应用和 PostgreSQL：

```bash
docker compose up --build

# 仅启动应用
docker compose up -d --build --no-deps app
```

应用默认访问地址为 `http://localhost:8080`，PostgreSQL 默认暴露在 `localhost:5432`。应用会在数据库就绪后启动并自动执行 migration。

后台启动、查看日志和停止：

```bash
docker compose up --build -d
docker compose logs -f app
docker compose down
```

`docker compose down` 会保留 PostgreSQL 数据卷。如需同时删除测试数据，使用 `docker compose down -v`。

以下为未来官方渠道扩展预留配置；当前启动流程不使用该 Token：

```bash
export APP_MCD_MCP_TOKEN='your-token'
docker compose up --build
```

### Hermes + MCP 统一容器

`cmd/mcp` 将推荐用例包装为 stdio MCP Server。`Dockerfile.hermes` 把该二进制放入 Hermes 官方镜像；Hermes 负责 MCP 子进程的生命周期，因此 MCP 不需要单独暴露端口。PostgreSQL 仍作为独立数据容器运行。

复制配置模板并生成随机密钥：

```bash
cp .env.hermes.example .env
openssl rand -hex 32
```

将生成值分别写入 `.env` 中的密码和密钥字段，并填写 `DEEPSEEK_API_KEY`。`POSTGRES_PASSWORD` 应使用 URL 安全字符；使用上面的十六进制输出即可。默认使用 DeepSeek 官方 API 的 `deepseek-v4-flash`；可通过 `HERMES_MODEL_NAME` 更换模型，通过 `DEEPSEEK_BASE_URL` 指向代理或兼容网关。

无需运行 `hermes setup`。模型、认证、Dashboard、API Server 和 MCP 配置均在容器启动时从 `.env` 注入：

```bash
docker compose -f compose.hermes.yaml up -d --build
```

镜像的初始化钩子会将模型选择同步到 `/opt/data/config.yaml`，并在 MCP 配置不存在时加入 stdio Server：

```yaml
model:
  provider: deepseek
  default: deepseek-v4-flash
mcp_servers:
  meal-recommendation:
    command: /usr/local/bin/start-meal-mcp
    enabled: true
```

完整的工具白名单、超时和串行调用设置由 `deploy/seed-hermes-config.py` 维护。`model.provider` 和 `model.default` 以 `.env` 为准，每次启动都会同步；已有 MCP 条目则保留 Dashboard 或 CLI 中的修改。DeepSeek API Key 只通过容器环境传入，不写入 `config.yaml`。

修改 `.env` 中的模型或 API 地址后，重建容器即可应用：

```bash
docker compose -f compose.hermes.yaml up -d --build --force-recreate
```

验证运行状态和 MCP 工具发现：

```bash
curl http://localhost:9119/api/status
curl http://localhost:8642/health
docker compose -f compose.hermes.yaml exec hermes hermes mcp test meal-recommendation
docker compose -f compose.hermes.yaml exec hermes hermes mcp list
```

浏览器访问 `http://localhost:9119/chat`，使用 `.env` 中的 Dashboard 用户名和密码登录。默认端口仅绑定宿主机 `127.0.0.1`。如果容器部署在远端服务器，保持该绑定，并从 Mac 建立隧道：

```bash
ssh -N -L 9119:127.0.0.1:9119 user@server
```

然后仍访问 `http://localhost:9119/chat`。

MCP 暴露以下只读目录查询和推荐会话工具，不暴露套餐发布、下架或批量导入等运营写操作：

- `list_product_catalog`
- `search_product_catalog`
- `start_recommendation_session`
- `get_recommendation_session`
- `update_meal_requirement`
- `generate_meal_recommendations`

### 本地运行

缺少工具链时可使用项目本地安装，不修改系统 Go：

```bash
scripts/install-go.sh
scripts/go.sh mod download
scripts/go.sh test ./...
# 可选：安装 Linux x86_64 C 编译器并执行竞态检查
scripts/install-c-compiler.sh
scripts/go.sh test -race -ldflags=-linkmode=external ./...
```

真实数据库测试需设置 `APP_TEST_DATABASE_URL`，未设置时相应测试跳过。
本地工具链和 Go 缓存保存在忽略的 `.build/` 下。

```bash
make check
make run
```

默认监听 `:8080`。可用环境变量：

- `APP_HTTP_ADDRESS`，默认 `:8080`
- `APP_SESSION_TTL`，默认 `30m`
- `APP_SHUTDOWN_TIMEOUT`，默认 `10s`
- `APP_DATABASE_URL`，PostgreSQL DSN；未设置时使用内存套餐库
- `APP_MCD_MCP_ENDPOINT`，默认 `https://mcp.mcd.cn`
- `APP_MCD_MCP_TOKEN`，麦当劳 MCP Bearer Token；未来官方渠道扩展预留；当前推荐不使用
- `APP_EXTERNAL_TIMEOUT`，默认 `12s`
- `APP_CACHE_TTL`，默认 `2m`
- `APP_CACHE_STALE_TTL`，默认 `30m`
- `APP_OFFER_STALE_AFTER`，默认 `24h`
- `APP_MCD_MAX_DETAILS`，单次菜单最多加载的套餐详情数，默认 `20`

## 当前 API

健康检查：

```bash
curl http://localhost:8080/healthz
```

创建推荐会话：

```bash
curl -X POST http://localhost:8080/v1/recommendation-sessions \
  -H 'Content-Type: application/json' \
  -d '{"orderMode":"pickup","city":"上海市","storeKeyword":"中山公园"}'
```

读取推荐会话：

```bash
curl http://localhost:8080/v1/recommendation-sessions/{sessionId}
```

更新结构化用餐需求：

```bash
curl -X PUT http://localhost:8080/v1/recommendation-sessions/{sessionId}/requirement \
  -H 'Content-Type: application/json' \
  -d '{
    "orderMode":"pickup",
    "location":{"storeCode":"store-1"},
    "mustHave":[
      {"resolvedProductId":"BIG_MAC","quantity":1},
      {"resolvedProductId":"MCFLURRY","quantity":1}
    ],
    "budget":{"amount":4000,"currency":"CNY"}
  }'
```

生成推荐：

```bash
curl -X POST http://localhost:8080/v1/recommendation-sessions/{sessionId}/recommendations
```

配置 `APP_DATABASE_URL` 后，程序启动时自动创建套餐表和不可变版本约束。未配置数据库时，运营 API 和推荐链路使用进程内套餐库；进程重启后数据会丢失。

### 标准单品库与运营 API

单品表示一个品牌下、一个明确规格的餐品。例如“薯条 / 中份”和“薯条 / 大份”是两个单品。`id` 是稳定的内部标识，也是套餐组成中的 `canonicalProductId`；不等同于官方渠道商品代码。

| 字段 | 含义 |
| --- | --- |
| `id` | 服务端生成的正数十进制雪花字符串，创建后不可修改；创建时不能指定 ID |
| `brand` | 必填，品牌代码，例如 `mcd`；去除首尾空格并转小写，最多 64 字符 |
| `name` | 必填，标准名称，例如“薯条”，最多 200 字符 |
| `specification` | 必填，规格，例如“中份”“大份”“5块”“标准份”，最多 100 字符 |
| `category` | 可选，分类，例如“小食”“汉堡”“饮料”，最多 100 字符 |
| `aliases` | 可选，搜索别名数组，例如 `["中薯", "中份薯条"]`；最多 50 项，每项最多 200 字符，自动去重 |
| `description` | 可选，运营备注，最多 2000 字符 |
| `price` | 可选参考单价，使用最小货币单位，例如 `{"amount":999900,"currency":"CNY"}` 表示 9999 元 |
| `salesScopes` | 销售范围数组：`standalone` 表示可单卖，`offer_component` 表示可作为套餐组件；创建时省略则默认两者都有 |
| `status` | 单品目录状态：`active` 或 `disabled`；创建默认 `active`。名称识别与目录浏览包含两种状态；停用不影响引用它的套餐，可购买性在推荐阶段判断 |
| `createdAt` / `updatedAt` | 服务端生成的 UTC 时间 |

同一品牌、名称、规格的组合不可重复（忽略大小写）；不同规格可以分别录入。单品的 `price` 是参考单价，不表示特定渠道的实时价格；渠道价、门店库存或优惠有效期不属于单品主数据。仅作为套餐组成出售的活动单品使用 `salesScopes:["offer_component"]`。当前实现单品主数据与套餐关联，官方商品代码映射和 MCP 自动对齐仍需后续实现。别名当前用于单品查询，不会自动解析推荐请求中的自然语言。

```text
POST /v1/admin/products              新增单品
PUT  /v1/admin/products/{productId}  修改单品（完整替换可编辑字段）
GET  /v1/admin/products/{productId}  查询单品详情
GET  /v1/admin/products              全量运营列表，包含启用和停用单品
GET  /v1/products                    名称识别与套餐选择用的全量列表，包含停用单品
POST /v1/products/search             批量按名称和规格搜索全部单品
```

列表直接返回 JSON 数组，无分页、无默认条数上限，空结果为 `[]`；按品牌、名称、规格、ID 升序排列。这里“全量”指本系统已录入的数据，不会自动抓取麦当劳全部菜单。两个列表均支持 `brand`、`category` 精确筛选，以及 `q` 对 ID、名称、规格、别名的子串搜索，忽略大小写。两个列表默认都不按状态过滤，也支持显式 `status=active|disabled` 筛选。不支持的查询参数（例如 `limit`、`page`）及重复参数返回 400。

批量搜索示例（MCP 工具 `search_product_catalog` 使用同样的请求和返回结构）：

```bash
curl -X POST http://localhost:8080/v1/products/search \
  -H 'Content-Type: application/json' \
  -d '{"brand":"mcd","queries":[{"name":"薯条","specification":"中份"},{"name":"薯条"},{"name":"不存在的商品"}]}'
```

`queries` 必须包含 1–100 项，每项 `name` 必填（最多 200 字符），`specification` 可选（最多 100 字符）；顶层 `brand` 是整批可选的精确品牌筛选（最多 64 字符）。参数去除首尾空白、忽略大小写；空规格表示不限规格，空品牌表示不限品牌。名称与显式规格必须分别等于同一商品的完整字段。本版不分词、不做子串、别名或模糊扩展；例如 `薯` 不匹配 `薯条`，`中` 不匹配 `中份`。请求不支持 URL 查询参数。

返回 `{"results":[...]}`，结果与输入逐项同序，重复查询也保留。每项包含原始 `query`、`status` 和 `candidates`（完整商品对象数组）：

- `exact`：恰好一个候选，额外返回该候选的 `resolvedProductId`。
- `ambiguous`：多个候选，不返回 `resolvedProductId`；需要调用方澄清，不能直接把候选当成用户允许替换的商品集合。
- `not_found`：零候选，`candidates` 为 `[]`，不返回 `resolvedProductId`。

搜索所有状态的商品，停用单品同样参与精确匹配和歧义判断；未指定品牌或规格时，同名的不同品牌/规格都保留。全词命中的相关性相同，按品牌、名称、规格、ID 升序稳定排序，不截断候选。整批仅加载一次目录快照，HTTP 和 MCP 复用相同应用层逻辑。三种业务状态均返回 HTTP 200；任一查询参数非法则整批返回 400，存储失败返回 500。搜索仅查询目录，不自动修改推荐需求，也不代表门店实时可售。

新增单品：

```bash
curl -X POST http://localhost:8080/v1/admin/products \
  -H 'Content-Type: application/json' \
  -d '{
    "brand":"mcd",
    "name":"薯条",
    "specification":"中份",
    "category":"小食",
    "aliases":["中薯","中份薯条"],
    "price":{"amount":1500,"currency":"CNY"},
    "salesScopes":["standalone","offer_component"]
  }'
```

返回 201、单品对象及 `Location` 详情地址。后续将返回的 `id` 原样填入套餐的 `components[].canonicalProductId`：

```json
{
  "canonicalProductId": "替换为新增单品返回的雪花ID",
  "quantity": 1
}
```

查询麦当劳全部可选单品及按别名搜索：

```bash
curl 'http://localhost:8080/v1/products?brand=mcd'
curl --get 'http://localhost:8080/v1/admin/products' \
  --data-urlencode 'brand=mcd' --data-urlencode 'q=中薯'
```

修改接口要求提供 `brand`、`name`、`specification`；省略 `category`、`aliases`、`description`、`price` 会清空这些字段，省略 `salesScopes` 或 `status` 则保留原值。传入 `status=disabled` 标记单品停用，但仍能在名称查询和套餐选择中被识别；传入 `active` 恢复单品状态。没有物理删除接口，以保留套餐引用。

套餐创建、草稿修改、CSV 导入、复核、发布及再核验只校验组成单品存在且包含 `offer_component` 销售范围，失败返回 `invalid_offer`（CSV 导入按行报告）。单品的 `active`/`disabled` 状态与套餐生命周期相互独立：停用单品不会影响已有套餐，也不会阻止包含它的套餐被创建、修改、复核、发布或再核验。

历史非雪花 ID 由数据库 migration 迁移，并同步单品及套餐引用。新增单品不能指定旧 ID，必须使用服务端返回的雪花 ID。名称或规格不明确时须先确认，服务不会根据历史 ID 自动猜测资料。

单品接口错误：参数不合法 400（`invalid_product`）、未找到 404（`product_not_found`）、ID 或名称规格重复 409（`product_conflict`）。配置数据库时使用 PostgreSQL 并自动创建 `products` 表；否则使用内存存储，重启丢失。

运行测试（PostgreSQL 测试在临时 schema 中运行并自动清理）：

```bash
go test -race ./...
APP_TEST_DATABASE_URL='postgres://meal:meal_dev_password@localhost:5432/meal_recommendation?sslmode=disable' go test ./internal/application/products -v
```

### 截图套餐录入

支持精确 N 选、重复选择上限、规格互斥、按次/按份加价、套餐防重和请求幂等。
Agent 可通过原有套餐 JSON API 显式提供缺失单品资料，服务在同一事务中精确复用或补建单品并保存草稿。
完整请求、响应、错误和兼容规则见 [截图套餐录入说明](docs/offer-ingestion-api.md)，
可运行示例见 [offer-ingestion.json](data/examples/offer-ingestion.json)。

### 运营套餐 API

```text
GET  /v1/admin/offers
POST /v1/admin/offers
PUT  /v1/admin/offers/{offerId}/draft
POST /v1/admin/offers/imports
POST /v1/admin/offers/{offerId}/review
POST /v1/admin/offers/{offerId}/publish
POST /v1/admin/offers/{offerId}/verify
POST /v1/admin/offers/{offerId}/disable
```

查询全部套餐（按最近更新时间倒序）：

```bash
curl http://localhost:8080/v1/admin/offers
```

创建“3 个主食中任选 2 个”，其中部分规格加价的套餐：

```bash
curl -X POST http://localhost:8080/v1/admin/offers \
  -H 'Content-Type: application/json' \
  -d '{
    "source":"meituan",
    "title":"主食三选二",
    "price":{"amount":2290,"currency":"CNY"},
    "choiceGroups":[
      {"id":"221458923456789010","name":"主食任选2件","minSelect":2,"maxSelect":2}
    ],
    "components":[
      {"canonicalProductId":"221458923456789011","quantity":1,"choiceGroupId":"221458923456789010"},
      {"canonicalProductId":"221458923456789012","quantity":1,"choiceGroupId":"221458923456789010","extraPrice":100},
      {"canonicalProductId":"221458923456789013","quantity":1,"choiceGroupId":"221458923456789010","extraPrice":400}
    ]
  }'
```

`choiceGroups` 定义选择规则；精确“m 中选 n”时将 `minSelect`、`maxSelect` 都设为 `n`。`components` 中没有 `choiceGroupId` 的是固定项，有该字段的是对应组的可选项。`extraPrice` 使用最小货币单位，并只在该项被选中时叠加到套餐基础 `price`；不同规格应作为不同标准单品录入，再分别设置加价。默认不能重复选择同一个组件；设置 `allowRepeat` 可启用重复，具体限制见截图套餐录入说明。单套餐最多展开 10,000 种合法组合，超过时创建或修改会被拒绝。

兼容旧数据：没有 `choiceGroups` 的历史套餐仍把所有组成项视为固定项，也不会开始收取历史上未生效的分组项 `extraPrice`；需要启用选择与加价时，应补录完整的选择组规则。

CSV 导入使用 `Content-Type: text/csv`，列为：

```text
source,title,price_amount,currency,purchase_url,city,store_codes,order_modes,valid_from,valid_until,valid_time_rules_json,choice_groups_json,components_json
```

`store_codes` 和 `order_modes` 使用 `|` 分隔；有效期使用 RFC3339。`valid_time_rules_json` 是可选 JSON 数组，星期使用 `0`（周日）到 `6`（周六），起止时刻使用 `HH:MM`，例如 `[{"weekdays":[1,2,3,4,5],"start":"10:30","end":"14:00"}]`；`choice_groups_json` 是可选的选择组 JSON 数组，`components_json` 是组成项 JSON 数组。

## 已预留的可替换边界

`internal/application/recommendation/ports.go` 定义了：

- `SessionRepository`
- `RequirementExtractor`
- `OfferCatalog`
- `OfficialCatalog`
- `OfficialPricing`
- `CandidateEngine`

真实数据源、自然语言需求抽取、商品映射和推荐快照等待办见 `todo.md`。

停用套餐如需修复内容，可通过 `PUT /v1/admin/offers/{offerId}/draft` 保存为草稿（保留版本序号），然后重新复核发布。内容无需修改的停用套餐可再次调用 `POST /v1/admin/offers/{offerId}/review` 进入复核状态，再调用 `publish` 发布新版本；不能从停用状态直接发布，已有发布快照保持不变。
