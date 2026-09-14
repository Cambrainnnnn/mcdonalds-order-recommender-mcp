# Screenshot offer ingestion implementation

## 当前恢复入口（2026-09-14）

以下状态优先于下方的历史检查点。用户已提供并授权本仓库 Git 身份：
`quinlan <quinlan1995@gmail.com>`，已设置 local config；提交阻塞解除。
本地 PostgreSQL 仍在 127.0.0.1:55432 运行。

提交安排：环境 → 共享选择规则 → 业务身份与数据库约束 → 聚合事务与持久化幂等 → HTTP → 使用文档。
持久化幂等与聚合事务共用原子边界，合并在第四个提交验证。
每个候选提交通过 index 导出到 `.build/commit-check/` 单独测试，保留主工作区全部实现；
测试通过后才提交。恢复时以 `git log` 和本节状态为准，不要重置主工作区。

- [已提交 2c1457c] 1. 环境脚本与基线：index 独立导出的完整测试（含 PostgreSQL）和 vet 均通过。
- [已提交 1a8f023] 2. 共享选择与计价规则：独立候选提交完整测试（含 PostgreSQL）通过。
- [已提交 009b965] 3. 套餐业务防重与迁移：完整测试（含 PostgreSQL）通过；HTTP externalId 与 409 映射补验通过。历史回填测试独立到 offer_identity_test.go。
- [已提交 0c5c9f0] 4. 单品/套餐原子保存与持久化幂等：独立候选完整 race 测试通过，含 PostgreSQL 多连接并发、回滚及发布后计价。
- [已提交 544d657] 5. HTTP 聚合录入及回归测试：独立候选 HTTP race 测试和全量 vet 通过。
- [完成，随本文提交] 6. 工作流程、示例与最终验证：完整 race 回归（含 PostgreSQL）及 vet 均通过；示例 JSON、脚本语法和 diff 检查通过。提交标题：`docs: document screenshot offer ingestion and recovery`。

### 恢复结论

本轮实现与六步提交任务均已完成，无待实现项或身份阻塞。
最新文档：[录入 API 与 agent 工作流程](offer-ingestion-api.md)；
示例：`data/examples/offer-ingestion.json`。后续继续开发时先查看 `git status` / `git log`，
不要按历史章节重新实现或重装依赖。测试命令：

```bash
APP_TEST_DATABASE_URL='postgres://meal_test@127.0.0.1:55432/postgres?sslmode=disable' \
  scripts/go.sh test -race -ldflags=-linkmode=external ./...
scripts/go.sh vet ./...
```


## 历史执行记录

以下内容保留历史事实，最新状态以上方恢复入口为准。

Implementation tasks (commit boundaries and current status are recorded below):

1. Reproducible local Go environment and baseline tests.
2. Shared selection/price rules: exact N-of-M, repetition with per-option caps,
   specification exclusivity, explicit per-selection/per-unit surcharge, limits.
3. Offer business deduplication and durable request idempotency, including concurrent writes.
4. Atomic product resolution/creation with offer saving in memory and PostgreSQL.
5. HTTP ingestion contract and end-to-end regression coverage.
6. Agent workflow, examples, compatibility documentation and final validation.

Design constraints:
- Keep existing POST offer and draft PUT routes; save drafts without auto-publishing.
- Product identity remains normalized brand/name/specification; aliases and OCR guesses
  must not silently merge identities. Missing products may be embedded explicitly.
- Offer deduplication uses a channel external ID when supplied and a normalized content
  fingerprint otherwise; retries use an idempotency key with payload mismatch detection.
- Failed aggregate writes must not leave newly created products behind.
- Preserve legacy published snapshots and default surcharge semantics.
- MCP remains a catalog/recommendation interface; agents save via the HTTP admin API.

## Development environment

Run `scripts/install-go.sh` on Linux x86_64, then `scripts/go.sh mod download`.
The verified toolchain and caches live in ignored `.build/`. Other platforms can use
an installed Go >= 1.25 with the same `scripts/go.sh` wrapper.

Run `scripts/go.sh test ./...` and `scripts/go.sh vet ./...`.
For real PostgreSQL tests, set `APP_TEST_DATABASE_URL` to a disposable PostgreSQL 16+
database with schema creation privileges. Tests isolate their data in temporary schemas.

## 恢复检查点（2026-09-13，用户主动暂停）

### 用户目标和约定

完整实现截图由 agent 识别后保存套餐：单品复用/缺失补建、套餐防重、
4选2/5选2、可重复/不可重复选、选项和规格加价；包括安装开发依赖。
每个 commit 完成一个可验证的小任务，不只做分析。暂停后先阅读本文恢复。
尚未修改业务代码，尚未创建任何 commit。当前 HEAD 为 `b1afd92`。

### 已完成

- 检查现有 HTTP、应用层、内存/PostgreSQL 仓储和推荐展开逻辑。
- Go 1.26.8 安装在 `.build/toolchains/go`，官方归档 SHA-256 已核验：
  `d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b`。
- `go mod download` 成功，缓存位于 `.build/gomodcache`，编译缓存 `.build/gocache`。
- 完整 `go test ./...` 基线通过（退出码 0）；未设置数据库 URL，真实 PostgreSQL
  集成测试被跳过，不能声称数据库验证已通过。尚未运行 vet。
- 新增未提交文件：本文、`scripts/go.sh`、`scripts/install-go.sh`。
- 下载并解压 PostgreSQL 16.4 到 `.build/postgres`，`postgres --version` 成功。
  来自 Maven Central 的
  `io/zonky/test/postgres/embedded-postgres-binaries-linux-amd64/16.4.0/embedded-postgres-binaries-linux-amd64-16.4.0.jar`。
- 已 initdb 到 `.build/pgdata`，用户 `meal_test`，trust 认证，默认数据库 `postgres`。
  初始化使用 `--no-locale`，编码 SQL_ASCII；恢复时可考虑另建 UTF8 测试集群。

### 当前环境阻塞

- 沙箱不允许网络/DNS，下载已通过 require_escalated 完成。
- Docker 存在但普通用户无 socket 权限；sudo 需要密码，容器没有启动成功。
  不要重复尝试 Docker/sudo。
- 本地 PostgreSQL 启动失败：`could not create IPv4 socket ... Operation not permitted`。
  日志 `.build/postgres.log`；请求沙箱外启动时用户中断了回合，不能视为已授权或启动成功。
- Go 测试后台 session 18373 的结果已收取，退出 0，无需再次轮询。
- `.git` 在当前权限配置中只读，后续 git add/commit 可能需要 require_escalated；
  用户已要求按小任务提交，但工具层权限仍需正常申请，不能绕过。

### 恢复顺序

1. 查看 git status 和本文，保留已有脚本，不重装 Go/依赖。
2. 运行 `scripts/go.sh vet ./...`，检查脚本后完成环境与基线 commit。
3. 通过正常权限流程启动本地测试 PostgreSQL（不使用生产数据库）：
   `.build/postgres/bin/pg_ctl -D .build/pgdata -l .build/postgres.log -o '-h 127.0.0.1 -p 55432 -k /tmp' start`
4. 数据库测试连接串可用：
   `postgres://meal_test@127.0.0.1:55432/postgres?sslmode=disable`。
   执行 `APP_TEST_DATABASE_URL=... scripts/go.sh test ./internal/adapters/outbound/postgres -count=1`。
   如沙箱阻止客户端连 socket，也按权限流程运行。
5. 继续前述 2–6 项编程任务；每项测试后提交，最后完整回归和说明 commit 列表。

### 已确认的代码事实（实现前须兼容）

- `internal/application/offers/service.go`：Create 每次生成 ID；没有套餐去重。
  ProductCatalog 目前只有 Get；validateProducts 只检查存在和 offer_component。
  runtime.go 已注入目录，但服务允许测试场景省略目录。
- `internal/adapters/inbound/httpapi/offers.go`：POST /v1/admin/offers、PUT draft、CSV 导入。
  JSON 严格拒绝未知字段。只有组件 ID，无内嵌单品资料。
- products 以规范化 brand/name/specification 唯一；数据库索引和内存锁均防重。
  重复创建返回 409，不返回已有对象；搜索不做别名/语义匹配。
- 选择组只有 minSelect/maxSelect；当前按组件行取不重复组合，重复商品行可绕过。
  quantity 是每次选择所得份数；extraPrice 每行加一次，不乘 quantity。
  没有 choiceGroups 的旧分组项视作固定项且不加历史 extraPrice，须保留旧快照兼容。
- 保存规则和推荐展开目前各自计数组合，上限 10,000；新规则应共用领域逻辑，避免分叉。
- 内存和 PostgreSQL 都有套餐到推荐 DTO 映射，新增字段必须贯穿两条链路。
- PostgreSQL migration 有 schema_migrations 和启动锁，新迁移不能改旧发布快照。
- 事务补建需要统一产品和套餐写入边界；不能简单先 CreateProduct 再 CreateOffer。
- MCP 当前只提供目录/推荐工具。当前设计继续通过 HTTP admin 保存，不随意扩充 MCP 写权限。
- README 有过时的自定义单品 ID 示例；当前只接受服务端生成的雪花 ID，选择组也要求数字字符串。

### 拟定设计，尚未实现

- 显式 allowRepeat、每选项次数上限、规格互斥，以及每次选择/每份加价模式。
- 用外部渠道商品 ID 和规范化内容指纹识别套餐；请求幂等键相同但载荷不同应冲突。
- 规范化指纹需忽略数组顺序/临时分组 ID 等无关差异，同时保留渠道、价格、有效期、
  选择和加价规则等业务差异；历史数据回填及并发唯一约束需要测试。
- 可内嵌确认的单品资料；事务中按唯一身份复用或补建，失败回滚，并返回补建/复用结果。
- 覆盖同请求重试、并发重复、无效规则回滚、重复商品行、规格互斥、加价数量及溢出、
  发布后推荐价格、数据库迁移和内存/PostgreSQL 一致性。

## 恢复执行进度

- 本地 PostgreSQL 已在 127.0.0.1:55432 启动；当前权限允许本地网络和 Git 写入。
- `scripts/go.sh vet ./...` 通过。设置测试数据库 URL 后完整 `test ./...` 通过，包含真实 PostgreSQL 集成测试。

## 当前实现检查点（继续实现后）

- 已实现共享领域选择展开：精确 N 选、重复次数上限、规格互斥、按次/按份加价、数量与金额溢出和搜索上限。
- 已实现渠道 externalId / 内容指纹防重，迁移回填历史身份且保留历史重复记录和发布快照。
- 已实现内存副本事务与 PostgreSQL 事务：单品精确复用/补建、草稿创建/修改、持久化幂等响应在同一提交边界。
- 已接入启动装配及原有 HTTP 路由；聚合请求使用 productBindings 和 Idempotency-Key。
- 已补充共享仓储一致性测试（并发同键重试、不同键业务防重、回滚、修改、发布后重放与计价）、HTTP 回归和迁移兼容测试。
- 文档与可运行示例：`docs/offer-ingestion-api.md`、`data/examples/offer-ingestion.json`。
- 完整普通测试（含 PostgreSQL）及 vet 曾通过；最终新增检查和 race 结果以本文后续记录为准。
- 安装了 SHA-256 核验的本地 Zig 0.13.0 作为 race 检查需要的 C 编译器；复现用 `scripts/install-c-compiler.sh`。
  `scripts/go.sh` 自动使用已安装的本地编译器，也可用 `CGO_ENABLED=0` 禁用。
- **尚未提交**：git commit 报 Author identity unknown，已向用户询问本仓库 user.name / user.email，等待回复。
  不要虚构用户身份。原计划按小任务提交仍待完成；当前 index 主要是环境脚本与计划文档。

### 本地竞态检查命令

本项目本地 Zig 编译器配合 Go race runtime 需要外部链接（默认内部链接缺少 `__popcountdi2`）：

```bash
APP_TEST_DATABASE_URL='postgres://meal_test@127.0.0.1:55432/postgres?sslmode=disable' \
  scripts/go.sh test -race -ldflags=-linkmode=external ./...
```

使用系统 GCC 时可沿用普通 `go test -race ./...`。

### 最终验证结果

- `CGO_ENABLED=0 APP_TEST_DATABASE_URL=... scripts/go.sh test ./...`：通过。
- `APP_TEST_DATABASE_URL=... scripts/go.sh test -race -ldflags=-linkmode=external ./...`：通过，包含真实 PostgreSQL 多连接并发测试。
- `scripts/go.sh vet ./...`、`git diff --check` 和安装脚本语法检查：通过。
- 测试夹具已改用唯一 ID，避免依赖旧内存仓储覆盖相同 ID 的行为。
- 功能实现、示例及文档完成；仍未创建 commit，仅待用户提供提交身份后按小任务拆分提交。
