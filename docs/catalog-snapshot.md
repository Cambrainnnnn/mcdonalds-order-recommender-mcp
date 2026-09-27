# 使用 Git 维护商品和套餐快照

本地人工采集、复核和发布仍通过现有运营 API 完成。维护后导出 `data/catalog.json`，检查 Git diff 并提交；其他环境启动时可按配置加载这份快照。不访问外部渠道，也不会在导入时刷新复核时间或延长套餐有效期。

## 导出本地 PostgreSQL

数据库应已完成当前版本的 migration。导出工具使用一致性只读事务，不执行 migration、不修改源数据库。

```sh
# 使用本地数据库的实际连接串；连接串不要提交到 Git。
export APP_DATABASE_URL='postgres://meal:YOUR_PASSWORD@127.0.0.1:5432/meal_recommendation?sslmode=disable'
make catalog-export
# 等价命令，可自定义路径和超时：
go run ./cmd/catalog -output data/catalog.json -timeout 1m
git diff -- data/catalog.json
```

Go 程序不自动读取 `.env`。`APP_DATABASE_URL` 优先于 `DATABASE_URL`。密码含特殊字符时需要 URL 编码，也可以使用 PostgreSQL keyword/value DSN。

重新构建后的标准 API / MCP HTTP 镜像包含 `catalog` 工具，可以使用 app 容器已有的数据库配置，不必暴露 PostgreSQL 端口：

```sh
docker compose exec -T app catalog -output /tmp/catalog.json
docker compose cp app:/tmp/catalog.json data/catalog.json
```

这两步顺序执行，第一步成功后再复制。`-output -` 可输出到 stdout，但直接使用 shell `>` 会在命令失败时截断目标文件，日常维护建议使用文件输出。文件输出使用同目录临时文件再原子替换，导出失败保留旧文件。

## 启动导入

三个入口 `cmd/api`、`cmd/mcp`、`cmd/mcp-http` 均支持：

| 环境变量 | 默认值 | 含义 |
| --- | --- | --- |
| `APP_CATALOG_SEED_MODE` | `off` | `off`、`if-empty` 或 `merge` |
| `APP_CATALOG_SEED_PATH` | 无；Compose 为 `/app/data/catalog.json` | 本地快照文件路径 |
| `APP_STARTUP_TIMEOUT` | `60s` | 数据库连接、migration、导入的总超时 |

`APP_STARTUP_TIMEOUT` 独立于外部请求使用的 `APP_EXTERNAL_TIMEOUT`。

- `off`：不读取文件，不导入数据，保持现有行为。
- `if-empty`：商品、套餐、发布历史均为空时导入；任一表已有数据则跳过，并在日志中记录 `loaded=false`。仍会检查文件是否存在、格式是否有效。
- `merge`：按原 ID 新增或更新商品、套餐，追加缺失的历史版本。文件未包含的记录保留；使用 `disabled` 表示下架。完整相同的记录跳过。

例如从源码启动：

```sh
APP_CATALOG_SEED_MODE=if-empty \
APP_CATALOG_SEED_PATH=data/catalog.json \
go run ./cmd/api
```

Docker 镜像将仓库 `data/` 复制到 `/app/data/`。更新快照后需重新构建和部署：

```sh
APP_CATALOG_SEED_MODE=merge docker compose up -d --build app
```

Railway 中设置 `APP_CATALOG_SEED_PATH=/app/data/catalog.json` 和相应模式。初次部署可使用 `if-empty`；持续同步 Git 中的数据使用 `merge`。Hermes Compose 也支持这三个变量。离线镜像构建会包含数据文件，但其 API 二进制仍须预先重新编译。

启用导入必须配置 PostgreSQL。服务在 migration 和导入完成后才监听请求。文件缺失、未知格式、关联错误或数据库冲突会使启动失败，导入事务全部回滚；已成功执行的 schema migration 不回滚。多进程导入及正常写入通过数据库表锁协调，普通读取不被阻塞。

## 冲突与版本规则

- 快照保留商品、套餐、版本和选择组的 ID；导入推进数据库 Snowflake 分配状态，避免后续 ID 冲突。
- 更新同一 ID 必须保留创建时间、使用更晚的 `updatedAt`。数据库套餐版本更高，或更新时间相同但内容不同，均报错。
- 相同历史版本必须内容完全一致；历史版本的更新/删除保护始终保持启用。新价格通过现有 API 复核、发布新版本，再重新导出。
- 新增或改变套餐业务身份时，如果与数据库中其他 ID 的套餐重复，导入报错；商品的品牌、名称、规格唯一约束同样保持生效。
- 快照必须包含商品引用及套餐完整发布历史。导出或导入发现缺失引用时明确报错，不自动伪造商品或补齐历史。
- 回退 Git 文件不等于数据库回滚。旧文件与新数据库冲突时需处理数据差异，不能靠重新启动强制覆盖。

建议让本地维护数据库成为目录数据的来源，其他部署使用这份 Git 快照。若多个数据库分别修改同一记录，先人工解决差异，再导出一致的快照。

## 文件格式与范围

`formatVersion: 1` 包含 `products`、`offers`、`offerVersions` 三个数组。固定排序和缩进，不写每次变化的导出时间；同一份业务数据反复导出内容一致。ID 使用字符串，金额使用整数分。时间统一为 UTC，保留真实复核时间及有效期。

业务去重索引在导入时按现有 migration 的规则重建，兼容历史重复记录。不导出 `schema_migrations`、`snowflake_state`、`offer_ingestion_requests`，也不包含内存推荐会话、数据库连接串或认证配置。导入到新库不保留旧采集请求的幂等响应。此文件是业务目录快照，不是全库备份。

## 验证

```sh
# 使用专门的测试数据库；测试在独立 schema 内运行并清理。
APP_TEST_DATABASE_URL='postgres://USER:PASSWORD@HOST:PORT/TEST_DB?sslmode=disable' go test ./...
go vet ./...
```

测试覆盖快照校验、导出/恢复一致性、重复导入、增量发布、冲突回滚、ID 分配、保留缺席记录和恢复后的推荐目录。未设置测试数据库时，PostgreSQL 集成测试跳过。
