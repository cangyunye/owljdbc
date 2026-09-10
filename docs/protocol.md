# owljdbc 帧协议

Go 驱动进程与 JVM sidecar（`owl.agent.Main`）之间通过 sidecar 的 stdin/stdout
以自定义二进制帧协议通信。全部整数小端（little-endian）。协议版本靠启动时的
`PING` 握手隐式确认，无独立版本号。

## 传输与启动

Go 侧启动 sidecar：

```
java -cp <AgentJar>:<driverJar1>:<driverJar2>... owl.agent.Main
```

- 请求：Go → sidecar **stdin**
- 响应：sidecar → Go **stdout**
- sidecar 日志：**stderr**（Go 侧原样转发到自己的 stderr，不影响帧流）

## 帧结构

```
[u32 totalLen][u8 type][payload ...]
```

- `totalLen` = 1（type 字节）+ len(payload)，**不含** 4 字节长度头本身。
- `totalLen` 取值 `1 <= totalLen <= 16 MiB`，越界报错并断开。
- `type` 取值：

| type | 值 | 方向 | 说明 |
|---|---|---|---|
| REQUEST | 0x01 | Go → sidecar | 控制/执行请求 |
| RESPONSE | 0x02 | sidecar → Go | 请求应答（非查询，或查询表头） |
| ROW_BATCH | 0x03 | sidecar → Go | 查询结果的一行 |
| END | 0x04 | sidecar → Go | 查询结束（含错误） |

sidecar 只处理 `REQUEST`，其余类型入站时被忽略。

## REQUEST payload

```
[u32 headerLen][header JSON (UTF-8)][binary arg1][binary arg2]...
```

header JSON 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | u32 | 请求序号，会话内单调递增；响应按此字段回填 |
| `conn` | u32 | 会话 id；`0` 专用于启动握手探针 |
| `op` | string | 操作名，见下表 |
| `sql` | string | SQL 文本（EXEC/QUERY 用） |
| `family` | string | `mysql` \| `oracle` \| `postgres`，影响取值渲染 |

二进制参数区紧跟 JSON 之后，按值编码逐個排列（见下）。

## 操作（op）

| op | 参数 | 应答 | 说明 |
|---|---|---|---|
| `PING` | — | RESPONSE | 探活；`conn=0` 时允许无连接，用于启动握手 |
| `CONNECT` | driverClass, url, user, pass | RESPONSE | 建 JDBC 连接并注册到 `conn` |
| `CLOSE` | — | RESPONSE | 关闭连接并移除会话 |
| `BEGIN` | — | RESPONSE | `setAutoCommit(false)` |
| `COMMIT` | — | RESPONSE | `commit()` 后恢复 autocommit |
| `ROLLBACK` | — | RESPONSE | `rollback()` 后恢复 autocommit |
| `CANCEL` | — | 无（best-effort） | 取消当前在读/执行的 Statement |
| `EXEC` | sql + 参数 | RESPONSE | 执行 DML/DDL，返回受影响行数 |
| `QUERY` | sql + 参数 | RESPONSE(表头) + ROW_BATCH* + END | 执行查询，流式返回 |
| `SHUTDOWN` | — | 无 | sidecar 退出 |

## RESPONSE / END payload

JSON（UTF-8），字段：

| 字段 | 说明 |
|---|---|
| `id` / `conn` | 回填请求的序号与会话 |
| `ok` | 成功标志 |
| `error` | 失败原因（`ok=false` 时） |
| `cols` | 仅 QUERY 表头：`[{"name":"...","type":"..."}]`，`type` 为原始数据库类型名（常量列可能为空串） |
| `rows` | 仅 END：累计返回行数 |
| `affected` | 仅 EXEC：受影响行数 |

## ROW_BATCH payload

```
[u32 conn][u32 id][u32 rowCount][value1][value2]...
```

当前实现 `rowCount` 恒为 `1`（一行一帧），为将来批量攒行预留。值区按列顺序编码。

## 值编码（小端）

每个值以 1 字节 tag 开头：

| tag | 值 | 载荷 |
|---|---|---|
| NULL | 0x00 | 无 |
| BOOL | 0x01 | 1 字节（0/1） |
| INT64 | 0x02 | 8 字节有符号 |
| FLOAT64 | 0x03 | 8 字节 IEEE-754 |
| DECIMAL | 0x04 | `[u32 len][UTF-8 文本]`（BigDecimal 的 `toPlainString()`） |
| STRING | 0x05 | `[u32 len][UTF-8 文本]` |
| BYTES | 0x06 | `[u32 len][原始字节]`（BLOB/二进制） |
| DATETIME | 0x07 | `[i64 epochMillis][i16 tzMinutes]` |
| DATE | 0x08 | 同字符串格式 |
| TIME | 0x09 | 同字符串格式 |

约定：

- `DATE` 在 Java 侧按当日午夜转成 `DATETIME` 编码，Go 侧还原为 `time.Time`，
  保证与原生驱动导出的 CSV 逐字节一致。
- `DATETIME` 的 `tzMinutes` 为本地时区偏移；Go 侧据此用 `time.FixedZone` 还原。
- Java 侧 `Date`（sql.Date）走 DATETIME 编码；`Time` 走 TIME 文本编码。

## 占位符改写

sidecar 在执行前对 SQL 做一次改写（`BindRewriter`）：把 Oracle 风格的 `:N`
绑定占位符替换为 JDBC 的 `?`。改写会跳过单引号字符串（含 `''` 转义）、双引号
标识符、`--` 行注释、`/* */` 块注释。PostgreSQL 的 `$N` 不改写（保持原生）。

## 会话与生命周期

- **进程复用**：1 个 classpath profile（classpath + JavaHome + AgentJar 指纹）
  对应 1 个 JVM，多连接复用；引用计数归零时 Kill 进程。
- **会话**：每个 `CONNECT` 分配一个 `conn` id；会话内语句串行执行。
- **取消**：Go 侧 ctx 取消时发 `CANCEL`（不等回包），随后丢弃该查询流。
- **失败快速传播（fail-fast）**：
  - 查询表头发出后中途失败，以 `END(ok=false)` 收尾，错误经 `Next()` 返回，
    不伪装成干净 EOF。
  - ROW_BATCH 解码失败时，用帧内独立的 12 字节头把错误路由回对应会话；
    连头都解析不出（协议级损坏）时中止所有会话。
  - sidecar 进程退出（stdout EOF）→ 所有在途请求/流立即报错。
