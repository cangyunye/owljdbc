// Package owljdbc exposes a database/sql driver named "owljdbc" that executes
// SQL through a JDBC driver running in a child JVM sidecar (owl.agent.Main).
//
// Import the package for its side effect of registering the driver, then open
// a connection with a JSON DSN built from a Config:
//
//	import (
//		"database/sql"
//		_ "github.com/cangyunye/owljdbc"
//	)
//
//	db, err := sql.Open("owljdbc", owljdbc.EncodeDSN(owljdbc.Config{
//		DriverClass: "com.mysql.cj.jdbc.Driver",
//		URL:         "jdbc:mysql://127.0.0.1:3306/app?characterEncoding=UTF-8",
//		User:        "root",
//		Password:    "***",
//		Family:      "mysql",
//		Classpath:   []string{"mysql-connector-j-8.0.33.jar"},
//		AgentJar:    "owl-agent.jar",
//	}))
//
// BuildConfig resolves DriverClass, JDBC URL, family and jar paths from the
// built-in per-database catalog, so callers only supply a parsed Endpoint.
//
// The sidecar JVM is spawned lazily on first connection and reused by every
// connection sharing the same classpath profile (classpath + JavaHome +
// AgentJar); it is killed once the last connection closes. The wire protocol
// is documented in docs/protocol.md.
package owljdbc
