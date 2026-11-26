# flywaygo

用 Go 管理 PostgreSQL 和 MySQL 的结构变更。扫描 SQL 目录，按版本执行还没成功过的脚本，并把结果记在历史表里。已经成功且内容没变的脚本不会重跑。

只支持 PostgreSQL 和 MySQL。模块路径：`github.com/linsu/flywaygo`。

## 能做什么

- 按版本执行 `V` 脚本，每个版本成功后只执行一次。
- 内容变化后重新执行 `R` 脚本。
- 用 `U` 脚本撤销一个已执行版本（仅库 API）。
- 记录版本、描述、脚本名、校验和、执行人和结果。
- 启动前校验已执行脚本是否被改过、本地是否缺失。
- 给已有库打基线，低于或等于基线的脚本不再执行。
- 修复历史：去掉失败记录，按当前文件回填校验和。
- 清空指定 schema 里的常见对象和历史表。默认关闭。
- 多进程启动时用锁排队，避免同一批脚本执行两次。
- 在迁移前后执行 SQL callback。
- 替换脚本中的 `${名字}`。

## 脚本

默认目录 `db/migration`。文件名用两个下划线分隔：

```text
db/migration/V1__create_users.sql
db/migration/V1.1__add_email.sql
db/migration/V2__add_orders.sql
db/migration/U2__drop_orders.sql
db/migration/R__active_users.sql
```

| 前缀 | 作用 |
|---|---|
| `V` | 版本化。按版本从小到大执行，成功一次后不再执行 |
| `R` | 可重复。校验和变化后再次执行，排在待执行的 `V` 脚本之后 |
| `U` | 撤销。只在调用 `Undo` 时使用 |

版本可以写成 `1`、`1.2`、`1.10`、`2026.11.26`。`1.10` 大于 `1.9`。`1.0` 与 `1` 相同。描述里的 `_` 会显示成空格，`-` 保留。

已在生产执行过的 `V` 文件不要改。要改结构就新增更高版本。改了已执行文件，下次启动会因校验和不一致失败。

## 在程序里用

调用方自己打开 `*sql.DB`，再交给 `core.New`。

```go
db, err := sql.Open("postgres", dsn)
if err != nil {
    return err
}
fw, err := core.New(ctx, core.Config{
    Driver:    "postgres",
    Locations: []string{"db/migration"},
    Database:  db,
}, nil)
if err != nil {
    return err
}
if err := fw.Migrate(ctx); err != nil {
    return err
}
```

MySQL 把 `Driver` 设为 `mysql`，并导入 `github.com/go-sql-driver/mysql`。库使用的 DSN 是驱动自己的格式。

`Migrate` 会创建配置中的 schema（默认允许）、创建历史表 `flyway_schema_history`、先校验再执行未成功的 `V` 脚本，然后执行内容已变化的 `R` 脚本。

其他方法：

```go
entries, err := fw.Info(ctx)
err = fw.Validate(ctx)
err = fw.Baseline(ctx) // 只写基线记录，不执行 SQL
err = fw.Repair(ctx)   // 不重放业务 SQL
err = fw.Undo(ctx)     // 需要对应的 U 脚本
err = fw.Clean(ctx)    // 默认被 CleanDisabled 拦住
```

布尔项用指针。不写就用默认值，要改成 false 时用 `core.Bool(false)`。

```go
core.Config{
    Schemas:           []string{"app"},
    DefaultSchema:     "app",
    Table:             "flyway_schema_history",
    BaselineOnMigrate: true,
    BaselineVersion:   "1",
    OutOfOrder:        false,
    TargetVersion:     "latest",
    ValidateOnMigrate: core.Bool(true),
    CleanDisabled:     core.Bool(true),
    CreateSchemas:     core.Bool(true),
    Group:             false,
    Placeholders:      map[string]string{"app_schema": "app"},
    Database:          db,
}
```

| 字段 | 默认 | 作用 |
|---|---|---|
| `Locations` | `db/migration` | SQL 目录 |
| `Schemas` | 当前 schema | 要管理的 schema，第一个放历史表 |
| `Table` | `flyway_schema_history` | 历史表名 |
| `BaselineOnMigrate` | false | 库非空且没有历史表时，先写基线再迁移 |
| `BaselineVersion` | `1` | 小于等于该版本的脚本视为已存在 |
| `OutOfOrder` | false | 允许补执行更低版本 |
| `TargetVersion` | `latest` | 迁移到该版本为止；写成 `3?` 时目标不存在也不失败 |
| `ValidateOnMigrate` | true | 迁移前校验 |
| `CleanDisabled` | true | 禁止清空 |
| `Group` | false | PostgreSQL 上把同一批版本化脚本放进一个事务 |
| `ExecuteInTransaction` | true | 单个脚本是否使用事务 |
| `IgnoreMigrationPatterns` | `*:future` | 校验时忽略的模式 |
| `LockRetryCount` | 50 | 拿不到锁时的重试次数，间隔默认 1 秒 |

空库直接 `Migrate`。已有表但没有历史表时：

```go
cfg.BaselineOnMigrate = true
cfg.BaselineVersion = "1"
```

## 占位符和 callback

`${app_schema}` 用 `Placeholders` 替换。没配置时可用 `${名字:默认值}`，分隔符默认 `:`。配置里的值优先，包括空字符串。`PlaceholderReplacement: core.Bool(false)` 关闭替换。

`beforeMigrate.sql`、`afterMigrate.sql`、`beforeEachMigrate.sql`、`afterEachMigrate.sql` 放在 `Locations` 或 `CallbackLocations`。同样有 validate、repair、clean、info、baseline、undo 的 before/after，以及 `afterConnect`。callback 不写入历史表。

## 命令行

```bash
go build -o bin/flywaygo ./cmd/flywaygo
```

```bash
bin/flywaygo migrate -url="postgres://app:pass@127.0.0.1:5432/appdb?sslmode=disable" -locations=db/migration
bin/flywaygo migrate -url="mysql://root:pass@tcp(127.0.0.1:3306)/appdb?parseTime=true" -locations=db/migration
bin/flywaygo info -url="..."
bin/flywaygo validate -url="..."
bin/flywaygo baseline -url="..." -baselineVersion=1
bin/flywaygo repair -url="..."
bin/flywaygo clean -url="..." -cleanDisabled=false
```

参数形式是 `-key=value`。也可以在工作目录放 `flyway.conf`：

```properties
flyway.url=postgres://app:pass@127.0.0.1:5432/appdb?sslmode=disable
flyway.locations=filesystem:db/migration
flyway.schemas=app
flyway.table=flyway_schema_history
flyway.baselineOnMigrate=true
```

环境变量 `FLYWAY_URL`、`FLYWAY_LOCATIONS`、`FLYWAY_USER`、`FLYWAY_PASSWORD` 同样生效。命令行覆盖环境变量，环境变量覆盖配置文件。

命令行支持 `migrate`、`info`、`validate`、`baseline`、`repair`、`clean`。不支持 `undo`。`help` 和 `version` 只打印信息。

命令行 URL 用 `postgres://` 或 `mysql://`。程序里 `sql.Open` 的 DSN 按驱动要求写，两者不要混用。

## 失败以后

PostgreSQL：失败的语句和对应历史一起回滚，历史表不留失败行。改好 SQL 后直接再执行 `migrate`。

MySQL：DDL 可能已经提交。历史表留下 `success=0`。处理残留对象后执行 `repair`，再 `migrate`。

`repair` 不重放业务 SQL。

## 限制

- 只有 PostgreSQL 和 MySQL。
- `clean` 删除表、视图、序列、例程、触发器、索引、类型等常见对象。不保证删掉所有自定义对象。
- 生产环境不要打开 `Clean`。

## 开发

```bash
go test ./...
```

目录：`core` 是库，`cmd/flywaygo` 是命令行，`demo` 是示例脚本。
