# owljdbc — JDBC 通道驱动（owl agent driver）

把「只有 JDBC 驱动」的数据库伪装成 Go `database/sql` 驱动：注册名 `owljdbc`，
底层通过子进程 JVM sidecar（`jvm/owl-agent`，自研 java.sql 封装）执行 JDBC。
帧协议见 [docs/protocol.md](docs/protocol.md)。

适用场景：目标/源库没有可用的 Go 原生驱动，只有厂商提供的 JDBC 驱动
（达梦、人大金仓、TimesTen、GoldenDB Oracle 模式等）。

## 环境要求

- Go 1.22+
- 运行期 JRE 8+（`java` 在 `PATH`，或用 `Config.JavaHome` 指定）
- 目标库的 JDBC 驱动 jar（下载脚本见 [scripts/fetch-jars.sh](scripts/fetch-jars.sh)）

## 安装

```bash
go get github.com/cangyunye/owljdbc
```

## 接入（两行）

```go
import _ "github.com/cangyunye/owljdbc" // init() 注册 "owljdbc" 驱动

db, err := sql.Open("owljdbc", owljdbc.EncodeDSN(owljdbc.Config{
    DriverClass: "com.oceanbase.jdbc.Driver",
    URL:         "jdbc:oceanbase://127.0.0.1:2881?useSSL=false&characterEncoding=UTF-8",
    User:        "root@obmysql",
    Password:    "***",
    Family:      "mysql",            // mysql | oracle | postgres
    Classpath:   []string{"oceanbase-client-2.4.1.jar"},
    AgentJar:    "owl-agent.jar",    // bash jvm/owl-agent/build.sh 构建
}))
```

只 import 不调用也行：包 `init()` 完成注册，通道在首次 `sql.Open` 后惰性 spawn
JVM sidecar（1 个 classpath profile = 1 个 JVM，多连接复用，全部关闭即回收）。

## Catalog（按库接入目录）

解析好连接要素（host/port/user/password/db）后可用内置目录组装配置，免手写
driverClass/URL：

```go
cfg, err := owljdbc.BuildConfig("oceanbase-mysql",
    owljdbc.Endpoint{Host: "127.0.0.1", Port: "2881", User: "root@obmysql",
        Password: "***", Database: "app"},
    "" /* dsn（TimesTen 等原样透传场景才需要） */, "./jars", "", "")
```

已登记：oceanbase-mysql / oceanbase-oracle（实测）、mysql / postgres（实测）、
opengaussdb（实测）、oracle / goldendb-mysql / goldendb-oracle / dm / kingbase /
timesten（登记未测）。接入新库 = 驱动 jar + family + URL 模板，无需改通道代码。

## 构建 sidecar

```bash
bash jvm/owl-agent/build.sh   # 产出 jvm/owl-agent/owl-agent.jar（JRE 8+ 可跑）
```

同一个 jar 服务任何宿主语言；换库只换 classpath 上的驱动 jar。

## 获取驱动 jar

```bash
bash scripts/fetch-jars.sh    # 下载 MySQL / PostgreSQL / OceanBase 驱动到仓库根目录
```

其他库的 jar 从厂商或 Maven Central 获取，放进 `jars_dir` 或 `Config.Classpath`。

## 测试

```bash
go test ./...                      # 单测（fake sidecar，无需 JVM/真库）
go test -tags e2e ./...            # 集成（需真库、JVM 与 jar，见测试内 env 变量）
```

## License

[MIT](LICENSE)
