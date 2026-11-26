# Flyway OSS 13.13.0 1:1 检查清单

基准：`flyway-core-13.13.0.jar`、`flyway-database-postgresql-13.13.0.jar`、`flyway-mysql-13.13.0.jar`。
范围：PostgreSQL / MySQL。商业版、Teams、Redgate 专属能力不在范围内。

图例：`[x]` 已具备并通过对应验证；`[~]` 已实现或已有测试但还要官方差分；`[ ]` 未完成；`[!]` 已确认差异。

最近全量验证：2026-11-23（`run-parity51.ps1` 90 场景 × 双引擎 = 145 条 PASS，其中 67 个场景由 `cases/` 提供夹具、其余内联定义；`run-installed-on51.ps1` 4 条 PASS）。

## A. 扫描、命名与版本
- [x] 默认后缀 `.sql`、递归扫描、目录排序和重复检测（basic / nested-desc 差分）
- [x] `V` / `U` / `R` 前缀、分隔符、版本解析、无描述和非法名称（validate-migration-naming 差分）
- [x] `validateMigrationNaming` 的收集与错误聚合（validate-migration-naming 差分）
- [x] 数字点版本比较、预定义版本 `<< Latest Version >>` / `<< Current Version >>` / `<< Next Version >>`（version-markers、baseline-new-* 差分）
- [x] `R1__x.sql`、`V1.sql`、空描述、多个点号等边界命名（nested-desc、info-repeat 差分）

## B. Checksum
- [x] Java CRC32 兼容、BOM/编码处理（verify 黄金值逐位一致）
- [x] versioned 使用原始资源 checksum（repair-version-checksum 差分）
- [x] repeatable 使用 placeholder 替换后的 checksum，并记录 equivalent checksum（mysql-repeat-repair、mysql-repeat-missing 差分）
- [x] `.conf` 覆盖编码和 placeholderReplacement（callback-conf 差分）
- [x] 负/正 checksum 比较和 Java 历史表 INTEGER 语义（repair-version-checksum 差分）

## C. Placeholder 与 SQL 脚本元数据
- [x] `${k}` 替换、前缀/后缀、未定义 placeholder、转义（placeholders-custom / -off / -empty / -undefined 差分）
- [x] `V1__x.sql.conf` 读取、编码、executeInTransaction、placeholderReplacement、shouldExecuteExpression（callback-conf 差分）
- [x] Java Properties 文件语义与配置优先级（config-* 系列差分）
- [x] 脚本级 `.conf` 对 callback / migration 的影响（callback-conf 差分）

## D. SQL Parser
- [x] 引号、转义、行注释、嵌套块注释、dollar-quoted body（pg-nested-comment、callback-parser-postgres 差分）
- [x] PostgreSQL `COPY ... FROM STDIN` 数据解析与执行（pg-copy 差分）
- [x] PostgreSQL 事务性判断（CONCURRENTLY、ALTER TYPE、VACUUM 等）（pg-concurrent、pg-execute-in-transaction-false 差分）
- [x] MySQL `DELIMITER`、stored procedure/function/trigger/event（mysql-routine-plain、callback-parser-mysql 差分）
- [x] MySQL 嵌套 `BEGIN/END`、`IF/LOOP/REPEAT/WHILE` 与 `CASE`（mysql-clean-trigger 差分）
- [x] 字符串、注释中的分号与跳版本 + repeatable 视图（sql-literals、sql-version-gap 差分，PG/MySQL 均 PASS）
- [x] Parser 错误消息类别、位置和截断行为（不要求本地化文本逐字一致）（pg-group-mixed-error 差分）

## E. 历史表
- [x] PostgreSQL / MySQL DDL 类型、默认值、主键和 success 索引（全场景历史表规范化比对）
- [x] 字段截断：description 200、script 1000、version/type/installed_by 长度（nested-desc 差分 + 单元测试）
- [x] installed_rank 生成规则，SCHEMA=0 与后续递增（pg-schema-marker、mysql-schema-marker 差分）
- [x] BASELINE、SCHEMA、DELETE、SQL_UNDO 合成记录内容（version-markers、repair-missing 差分）
- [x] `installed_on` 本地时区显示差异已收敛：双引擎/双实现精确比对 InstalledOn / InstalledOnUTC（Asia/Shanghai）4 条全 PASS
- [x] 历史表不存在、空表、损坏记录和 future/missing 状态（info-states、pg-schema-missing-history、mysql-schema-missing-history 差分）

## F. Migrate / Transaction
- [x] 非空库无历史表、baselineOnMigrate、空库 baseline marker 规则（baseline-on-migrate-* 差分）
- [x] `beforeMigrate`、每个迁移回调、repeatable 分界、after 回调（callback-flow、callbacks-all 差分）
- [x] PostgreSQL DDL 事务与失败整体回滚（pg-group-rollback、pg-failed 差分）
- [x] MySQL 无 DDL 事务、失败记录 `success=0`（mysql-failed、mysql-repair-failed 差分）
- [x] group=true/false、target、outOfOrder、后续循环刷新（target-*、out-of-order、mysql-mix 差分）
- [x] `executeInTransaction=false` 与不可事务语句（pg-execute-in-transaction-false 差分）
- [x] 并发迁移锁、rank 连续、不同进程不重复执行（pg-concurrent 差分 + core 集成测试）

## G. Info / Validate
- [x] resolved/applied 合并、状态排序、category/type/version/script/checksum/rank（info-states、info-repeat 差分）
- [x] PENDING、SUCCESS、FAILED、MISSING_*、FUTURE_*、OUT_OF_ORDER、SUPERSEDED、UNDONE、DELETED（info-states 差分）
- [x] `info` 不受 ignoreMigrationPatterns 过滤（out-of-order-info 差分）
- [x] `validate` 默认 pending 校验、ignore patterns、target、no-history 行为（ignore-patterns、target-missing-required 差分）
- [x] `validateWithResult` 版本、description、path、errorCode、validateCount（validate 内部协议差分）
- [x] `validateOnMigrate` 与回调事件顺序（validate-on-migrate-false 差分）

## H. Repair
- [x] 删除失败记录（mysql-repair-failed、repair-missing 差分）
- [x] missing/future 的 DELETE marker 与 ignore patterns（repair-missing、repair-missing-versioned 差分）
- [x] 修正 versioned description/type/checksum（repair-version-checksum 差分）
- [x] repeatable equivalent checksum 修正（mysql-repeat-repair 差分）
- [x] baseline、superseded、deleted、undone 跳过规则（baseline-existing-history-* 差分）

## I. Baseline / Clean / Undo
- [x] baseline 新建历史表、已存在历史表、重复 baseline、已有迁移拒绝（baseline-new-*、baseline-existing-*、baseline-reject-* 差分）
- [x] baseline 特殊版本 0 与 schema marker 冲突（baseline-new-zero、pg-schema-marker-baseline-conflict、mysql-schema-marker-baseline-conflict 差分）
- [x] PG/MySQL clean 对象覆盖、顺序和 cleanDisabled（pg-clean、pg-clean-complex、mysql-clean、mysql-clean-trigger、pg-schema-clean-created 差分）
- [~] undo 找最新版本、SQL_UNDO、DELETE marker、UNDONE 状态（Core API 已实现并有单元/集成测试；官方 OSS CLI 拒绝 undo，无官方差分基准）

## J. Callbacks
- [x] 默认从 migration locations 扫描；callbackLocations 覆盖（callback-default-location 差分）
- [x] 事件名、排序、重复 callback 检测、placeholder（callback-duplicate、callback-placeholders 差分）
- [x] migrate/validate/repair/clean/info 的成功与错误事件（callback-error-event 差分）
- [x] statement 级事件（statement-callbacks、statement-callbacks-error 差分）

## K. CLI 与配置
- [x] 参数名称、默认值、布尔解析、逗号列表（cli_user golden 测试）
- [x] JDBC / 简短 url 解析、驱动推断（config-* 差分 + cli 单元测试）
- [x] 配置文件和 SDK Config 的等价性（config-file-only、config-env-overrides-file、config-cli-overrides-env、config-working-directory、config-placeholder-equals、config-env-expansion、config-default-layers、config-unknown-key 差分）
- [x] 完整 `flyway` CLI 子命令帮助与退出码对齐（OSS 范围）：help/-?/-h/--help/help check/help migrate、version、list-engines、banner、非法 flag、`-X`、`-q`、json、outputFile 逐字节 golden；undo/deploy/check 等商业命令退出码 35

## L. 官方差分自动化
- [x] 官方 JAR 与 Java harness 资产
- [x] 统一 `ParityRunner`（migrate/info/validate/repair/baseline/clean + callbacks/placeholders/配置）
- [x] Go `dbprobe`：重建库、规范化历史表、普通对象快照
- [x] PowerShell 差分脚本：每场景重置、双引擎分别执行、PASS/FAIL 汇总（PS7 `run-parity.ps1` + PS5.1 兼容 `run-parity51.ps1`）
- [x] 全场景双库绿；真实差异逐项关闭（2026-11-23：149/149 PASS，`undo` OSS 无官方基准保持 `[~]`）
- [x] README 更新为当前能力、测试入口和已知限制

## 明确不作为 1:1 范围
- [x] 商业版 / Teams / Redgate 专属命令与功能
- [x] 非 PostgreSQL / MySQL 数据库
- [x] 与迁移语义无关的日志本地化、耗时文本、stacktrace 文本逐字一致
