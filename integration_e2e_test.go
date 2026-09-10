//go:build e2e
// +build e2e

package owljdbc

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ── dev-env 读取（OS env > testdata/db/.local-dev.env） ──

func devEnvMap() map[string]string {
	m := map[string]string{}
	data, err := os.ReadFile(filepath.Join("testdata", "db", ".local-dev.env"))
	if err == nil {
		re := regexp.MustCompile(`(?m)^export\s+([A-Za-z0-9_]+)='(.*)'\s*$`)
		for _, mm := range re.FindAllStringSubmatch(string(data), -1) {
			m[mm[1]] = mm[2]
		}
	}
	return m
}

func lookupEnv(m map[string]string, key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return m[key]
}

type jdbcCred struct {
	User, Password, Host string
	Port                 int
}

// oceanbase-oracle://MIGSRC@oratest:PASS%40WORD@127.0.0.1:2881/
func parseOracleURLDSN(t *testing.T, dsn string) jdbcCred {
	t.Helper()
	rest := strings.TrimPrefix(strings.TrimPrefix(dsn, "oceanbase-oracle://"), "oracle://")
	at := strings.LastIndex(rest, "@") // 口令百分号编码后，最后一个 @ 分隔 userinfo/host
	if at < 0 {
		t.Fatalf("bad oracle dsn")
	}
	userinfo, hostport := rest[:at], rest[at+1:]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		t.Fatalf("bad oracle userinfo")
	}
	pass, err := url.PathUnescape(userinfo[colon+1:])
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	hp := strings.TrimSuffix(hostport, "/")
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		t.Fatalf("bad host:port")
	}
	var port int
	fmt.Sscanf(hp[i+1:], "%d", &port)
	return jdbcCred{User: userinfo[:colon], Password: pass, Host: hp[:i], Port: port}
}

// root@obmysql:PASS@WORD@tcp(127.0.0.1:2881)/
func parseMysqlWireDSN(t *testing.T, dsn string) jdbcCred {
	t.Helper()
	i := strings.Index(dsn, "@tcp(")
	if i < 0 {
		t.Fatalf("bad mysql dsn")
	}
	userinfo := dsn[:i]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		t.Fatalf("bad mysql userinfo")
	}
	inside := dsn[i+len("@tcp("):]
	if j := strings.Index(inside, ")"); j >= 0 {
		inside = inside[:j]
	}
	inside = strings.Split(inside, "/")[0]
	hp := inside
	j := strings.LastIndex(hp, ":")
	if j < 0 {
		t.Fatalf("bad host:port")
	}
	var port int
	fmt.Sscanf(hp[j+1:], "%d", &port)
	return jdbcCred{User: userinfo[:colon], Password: userinfo[colon+1:], Host: hp[:j], Port: port}
}

func e2eAgentCfg(t *testing.T, family, user, pass, host string, port int) Config {
	t.Helper()
	agentJar := os.Getenv("OWL_AGENT_JAR")
	if agentJar == "" {
		agentJar = filepath.Join("jvm", "owl-agent", "owl-agent.jar")
	}
	obJar := os.Getenv("OWL_E2E_OB_JAR")
	if obJar == "" {
		obJar = filepath.Join("oceanbase-client-2.4.1.jar")
	}
	if _, err := os.Stat(agentJar); err != nil {
		t.Skipf("agent jar missing (%s): run bash jvm/owl-agent/build.sh", agentJar)
	}
	if _, err := os.Stat(obJar); err != nil {
		t.Skipf("OB jar missing (%s)", obJar)
	}
	return Config{
		DriverClass: "com.oceanbase.jdbc.Driver",
		URL:         fmt.Sprintf("jdbc:oceanbase://%s:%d?useSSL=false", host, port),
		User:        user,
		Password:    pass,
		Family:      family,
		Classpath:   []string{obJar},
		AgentJar:    agentJar,
	}
}

// ── OB Oracle 租户冒烟 ──

func TestE2E_AgentOBOracleSmoke(t *testing.T) {
	env := devEnvMap()
	dsn := lookupEnv(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN (or testdata/db/.local-dev.env)")
	}
	cred := parseOracleURLDSN(t, dsn)
	db, err := sql.Open("owljdbc", EncodeDSN(e2eAgentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var v string
	if err := db.QueryRowContext(context.Background(), "SELECT 'OK' FROM DUAL").Scan(&v); err != nil {
		t.Fatalf("query: %v", err)
	}
	if v != "OK" {
		t.Fatalf("got %q", v)
	}
	// sql 字段含逗号 → 走 Json 解析器的字符串保护路径
	if err := db.QueryRowContext(context.Background(), "SELECT 'a,b' FROM DUAL").Scan(&v); err != nil {
		t.Fatalf("comma query: %v", err)
	}
	if v != "a,b" {
		t.Fatalf("comma got %q", v)
	}
}

// ── OB MySQL 租户冒烟 ──

func TestE2E_AgentOBMySQLSmoke(t *testing.T) {
	env := devEnvMap()
	dsn := lookupEnv(env, "OWL_E2E_OB_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_MYSQL_DSN (or testdata/db/.local-dev.env)")
	}
	cred := parseMysqlWireDSN(t, dsn)
	db, err := sql.Open("owljdbc", EncodeDSN(e2eAgentCfg(t, "mysql", cred.User, cred.Password, cred.Host, cred.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var v string
	if err := db.QueryRowContext(context.Background(), "SELECT 'OK'").Scan(&v); err != nil {
		t.Fatalf("query: %v", err)
	}
	if v != "OK" {
		t.Fatalf("got %q", v)
	}
	if err := db.QueryRowContext(context.Background(), "SELECT 'a,b'").Scan(&v); err != nil {
		t.Fatalf("comma query: %v", err)
	}
	if v != "a,b" {
		t.Fatalf("comma got %q", v)
	}
}

// ── 参数绑定：oracle :N（agent 侧改写为 ?） + mysql ?；多行流式 ──

func TestE2E_AgentBindArgs(t *testing.T) {
	env := devEnvMap()
	odsn := lookupEnv(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	mdsn := lookupEnv(env, "OWL_E2E_OB_MYSQL_DSN")
	if odsn == "" && mdsn == "" {
		t.Skip("set OB tenant DSNs")
	}
	if odsn != "" {
		cred := parseOracleURLDSN(t, odsn)
		db, err := sql.Open("owljdbc", EncodeDSN(e2eAgentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port)))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var n int64
		if err := db.QueryRowContext(context.Background(), "SELECT :1 FROM DUAL", int64(41)).Scan(&n); err != nil {
			t.Fatalf("oracle bind: %v", err)
		}
		if n != 41 {
			t.Fatalf("oracle bind got %d", n)
		}
	}
	if mdsn != "" {
		cred := parseMysqlWireDSN(t, mdsn)
		db, err := sql.Open("owljdbc", EncodeDSN(e2eAgentCfg(t, "mysql", cred.User, cred.Password, cred.Host, cred.Port)))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		rows, err := db.Query("SELECT ? UNION ALL SELECT ?", int64(1), int64(2))
		if err != nil {
			t.Fatalf("mysql multi-row: %v", err)
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var n int64
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("rows = %d, want 2", count)
		}
	}
}

// ── 启动失败快速失败（坏 jar 路径） ──

func TestE2E_AgentBadJarFailsFast(t *testing.T) {
	env := devEnvMap()
	dsn := lookupEnv(env, "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	if dsn == "" {
		t.Skip("set OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	}
	cred := parseOracleURLDSN(t, dsn)
	cfg := e2eAgentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port)
	cfg.AgentJar = "/nonexistent/owl-agent.jar"
	db, err := sql.Open("owljdbc", EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.QueryContext(context.Background(), "SELECT 1 FROM DUAL"); err == nil {
		t.Fatal("expected error for missing agent jar")
	}
}
