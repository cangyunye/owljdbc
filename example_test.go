package owljdbc_test

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/cangyunye/owljdbc"
)

// 两行接入：import 注册驱动，sql.Open 打开 agent 通道。
// JVM sidecar 在首次连接时惰性 spawn，连接池多连接复用同一进程。
func Example() {
	db, err := sql.Open("owljdbc", owljdbc.EncodeDSN(owljdbc.Config{
		DriverClass: "com.oceanbase.jdbc.Driver",
		URL:         "jdbc:oceanbase://127.0.0.1:2881/app?useSSL=false&characterEncoding=UTF-8",
		User:        "root@obmysql",
		Password:    "***",
		Family:      "mysql",
		Classpath:   []string{"oceanbase-client-2.4.1.jar"},
		AgentJar:    "owl-agent.jar",
	}))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil {
		log.Fatal(err)
	}
	fmt.Println(one)
}
