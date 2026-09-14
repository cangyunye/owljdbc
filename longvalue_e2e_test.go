//go:build e2e
// +build e2e

package owljdbc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Oracle LONG 值 + 同连接顺序查询回归（修复 4cc00d2：QUERY 完成时先关 rs/ps 再发
// 完成帧）。修复前 Oracle LONG 的分段流会被上一条查询延迟的 close 误伤，同连接
// 第二条 LONG 查询报 ORA-17027。fake-agent 模拟不了 LONG 分段流，单测对此类 bug
// 天然免疫，必须在真库上固化。
//
// 环境变量（OS env > testdata/db/.local-dev.env，每行 export KEY='value'）：
//   OWL_E2E_ORACLE_DSN            真 Oracle，oracle://user:pass@host:port/服务名
//   OWL_E2E_MYSQL_DSN             MySQL，mysql://user:pass@host:port/库
//   OWL_E2E_PG_DSN                PostgreSQL，postgresql://user:pass@host:port/库
//   OWL_E2E_OB_ORACLE_MIGSRC_DSN  OB Oracle 租户（只读即可）
//   OWL_E2E_OB_ORACLE_TEST_DSN    OB 可写租户（建删 OWL_E2E_LV_ 前缀临时表）
// Oracle 用例按 真 Oracle > OB 租户 的顺序取第一个非空 DSN；密码按 URL 百分号编码。

// ── 通用 helper ──

// parseURLDSN 解析 scheme://user:pass@host:port/db 形式的 URL DSN，返回连接
// 凭证与库名/服务名（密码按 URL 百分号解码）。
func parseURLDSN(t *testing.T, dsn string) (jdbcCred, string) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" || u.User == nil {
		t.Fatalf("bad url dsn %q: %v", dsn, err)
	}
	pass, _ := u.User.Password() // 原始百分号编码形式，需手动解码
	if dec, err := url.PathUnescape(pass); err == nil {
		pass = dec
	}
	port := u.Port()
	if port == "" {
		t.Fatalf("url dsn %q missing port", dsn)
	}
	var portNum int
	fmt.Sscanf(port, "%d", &portNum)
	return jdbcCred{User: u.User.Username(), Password: pass, Host: u.Hostname(), Port: portNum},
		strings.TrimPrefix(u.Path, "/")
}

// buildProfileCfg 用生产 catalog profile 组装连接配置；驱动 jar / agent jar
// 缺失按环境未就绪处理（skip），与既有 e2eAgentCfg 的语义一致。
func buildProfileCfg(t *testing.T, dbType, dsn string) Config {
	t.Helper()
	cred, database := parseURLDSN(t, dsn)
	agentJar := os.Getenv("OWL_AGENT_JAR")
	if agentJar == "" {
		agentJar = filepath.Join("jvm", "owl-agent", "owl-agent.jar")
	}
	cfg, err := BuildConfig(dbType, Endpoint{
		Host: cred.Host, Port: strconv.Itoa(cred.Port),
		User: cred.User, Password: cred.Password, Database: database,
	}, "", ".", agentJar, "")
	if err != nil {
		t.Skipf("build %s config (driver jar missing?): %v", dbType, err)
	}
	return cfg
}

func openWithCfg(t *testing.T, cfg Config) *sql.DB {
	t.Helper()
	db, err := sql.Open("owljdbc", EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// openOracleE2E 依次尝试 dsnKeys，第一个非空的生效：oracle:// 走生产 catalog
// profile（真 Oracle），oceanbase-oracle:// 走既有 e2eAgentCfg（OB 租户）。
func openOracleE2E(t *testing.T, env map[string]string, dsnKeys ...string) *sql.DB {
	t.Helper()
	for _, key := range dsnKeys {
		dsn := lookupEnv(env, key)
		if dsn == "" {
			continue
		}
		if strings.HasPrefix(dsn, "oceanbase-oracle://") {
			cred := parseOracleURLDSN(t, dsn)
			return openWithCfg(t, e2eAgentCfg(t, "oracle", cred.User, cred.Password, cred.Host, cred.Port))
		}
		return openWithCfg(t, buildProfileCfg(t, "oracle", dsn))
	}
	t.Skipf("set one of %v (or testdata/db/.local-dev.env)", dsnKeys)
	return nil
}

// openFamilyE2E 打开 mysql/postgres 等仅支持 URL 风格 DSN 的连接池，
// 依次尝试 dsnKeys，第一个非空的生效。
func openFamilyE2E(t *testing.T, env map[string]string, dbType string, dsnKeys ...string) *sql.DB {
	t.Helper()
	for _, key := range dsnKeys {
		if dsn := lookupEnv(env, key); dsn != "" {
			return openWithCfg(t, buildProfileCfg(t, dbType, dsn))
		}
	}
	t.Skipf("set one of %v (or testdata/db/.local-dev.env)", dsnKeys)
	return nil
}

// pinnedConn 钉住一条物理连接：一个 *sql.Conn 对应 agent 侧一个 Session/conn-id，
// "同连接第二条查询"必须落在同一 conn-id 上才算复现。
func pinnedConn(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// dropTable 尽力删表：忽略一切错误（表不存在、回收站语义等），仅用于清理。
func dropTable(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	_, _ = db.ExecContext(context.Background(), "DROP TABLE "+table)
}

// deterministicPayload 生成长度 n 的确定性 ASCII 文本：重跑对拍稳定，截断处
// 也不会切在多字节字符中间。
func deterministicPayload(n int) string {
	var sb strings.Builder
	sb.Grow(n)
	for i := 0; sb.Len() < n; i++ {
		fmt.Fprintf(&sb, "%06d:abcdefghijklmnopqrstuvwxyz", i)
	}
	return sb.String()[:n]
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum)
}

// ── all_tab_columns.data_default（LONG 列）候选发现 ──

type longDefaultCol struct{ Owner, Table, Column string }

func (c longDefaultCol) String() string {
	return fmt.Sprintf("%s.%s.%s", c.Owner, c.Table, c.Column)
}

type longProbe struct {
	col    longDefaultCol
	sample string
}

var errNoDefaultCandidate = errors.New("no non-empty data_default candidate")

// findLongDefault 发现一个可用于 LONG 回归的 data_default 列并返回样例值。
// 全部候选查询失败时返回错误——修复前的 jar 上，探测中途开始整批报 ORA-17027，
// 这正是症状本身，必须浮出而非退化成 skip；租户里没有非空 data_default 时 skip。
func findLongDefault(t *testing.T, ctx context.Context, conn *sql.Conn) (longProbe, error) {
	t.Helper()
	cands, err := listDefaultCandidates(t, ctx, conn)
	if err != nil {
		return longProbe{}, err
	}
	return pickLongDefault(t, ctx, conn, cands)
}

// listDefaultCandidates 找带默认值的列。个别兼容实现不允许对 LONG 列用
// IS NOT NULL，此时退化为抽样探测。
func listDefaultCandidates(t *testing.T, ctx context.Context, conn *sql.Conn) ([]longDefaultCol, error) {
	t.Helper()
	rows, err := conn.QueryContext(ctx,
		"SELECT owner, table_name, column_name FROM all_tab_columns WHERE data_default IS NOT NULL AND ROWNUM <= 40")
	if err == nil {
		return collectCandidates(rows), nil
	}
	t.Logf("data_default IS NOT NULL rejected (%v); probing sample rows instead", err)
	rows, err = conn.QueryContext(ctx,
		"SELECT owner, table_name, column_name FROM all_tab_columns WHERE ROWNUM <= 80")
	if err != nil {
		return nil, err
	}
	return collectCandidates(rows), nil
}

func collectCandidates(rows *sql.Rows) []longDefaultCol {
	defer rows.Close()
	var out []longDefaultCol
	for rows.Next() {
		var c longDefaultCol
		if err := rows.Scan(&c.Owner, &c.Table, &c.Column); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// pickLongDefault 逐个试查 data_default，返回第一个 ≥ 20 字节的候选（Oracle
// LONG 在 inline 阈值之上才走分段流多块路径）；没有达标的则返回第一个非空的。
func pickLongDefault(t *testing.T, ctx context.Context, conn *sql.Conn, cands []longDefaultCol) (longProbe, error) {
	t.Helper()
	var fallback longProbe
	var firstErr error
	for _, c := range cands {
		v, err := queryDataDefault(ctx, conn, c)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if v == "" {
			continue
		}
		if fallback.sample == "" {
			fallback = longProbe{col: c, sample: v}
		}
		if len(v) >= 20 {
			return longProbe{col: c, sample: v}, nil
		}
	}
	if fallback.sample != "" {
		t.Logf("no data_default >= 20 bytes among %d candidates; using %s (%d bytes, may miss the segmented-stream path)",
			len(cands), fallback.col, len(fallback.sample))
		return fallback, nil
	}
	if firstErr != nil {
		return longProbe{}, firstErr
	}
	return longProbe{}, fmt.Errorf("%w among %d candidates", errNoDefaultCandidate, len(cands))
}

// queryDataDefault 按原始复现的查法读单个列的 data_default。
func queryDataDefault(ctx context.Context, conn *sql.Conn, c longDefaultCol) (string, error) {
	var v sql.NullString
	err := conn.QueryRowContext(ctx,
		"SELECT data_default FROM all_tab_columns WHERE owner = :1 AND table_name = :2 AND column_name = :3",
		c.Owner, c.Table, c.Column).Scan(&v)
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// ── P0.1 原始 bug 场景：同连接先任意查询，再连查 LONG 列两次 ──

func TestE2E_AgentOBOracleLongSameConnRepro(t *testing.T) {
	env := devEnvMap()
	db := openOracleE2E(t, env, "OWL_E2E_ORACLE_DSN", "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn := pinnedConn(t, db)

	// 先跑任意查询（原始复现的第一步）
	var warm string
	if err := conn.QueryRowContext(ctx, "SELECT 'WARM' FROM DUAL").Scan(&warm); err != nil {
		t.Fatalf("warm-up query: %v", err)
	}

	probe, err := findLongDefault(t, ctx, conn)
	if err != nil {
		t.Fatalf("probe data_default: %v", err) // 修复前的 jar：这里就是 ORA-17027
	}

	// 连续两次查同一 LONG 列，第二次是关键：修复前报 ORA-17027
	first, err := queryDataDefault(ctx, conn, probe.col)
	if err != nil {
		t.Fatalf("first LONG query on %s: %v", probe.col, err)
	}
	second, err := queryDataDefault(ctx, conn, probe.col)
	if err != nil {
		t.Fatalf("second LONG query on %s (the critical one): %v", probe.col, err)
	}
	if first != second {
		t.Fatalf("LONG value drifted between consecutive queries on %s: %d vs %d bytes, hashes %s vs %s",
			probe.col, len(first), len(second), hashOf(first), hashOf(second))
	}
	t.Logf("OK: %s data_default %d bytes, two consecutive same-conn queries consistent", probe.col, len(first))

	// 收尾普通查询：连接完全可用
	var tail string
	if err := conn.QueryRowContext(ctx, "SELECT 'TAIL' FROM DUAL").Scan(&tail); err != nil {
		t.Fatalf("tail query after LONG reads: %v", err)
	}
}

// ── P0.2 大文本完整性：LONG/CLOB >8KB、>64KB 读回对拍（覆盖分段流多块路径）──

func TestE2E_AgentOBOracleLongLargeIntegrity(t *testing.T) {
	env := devEnvMap()
	db := openOracleE2E(t, env, "OWL_E2E_ORACLE_DSN", "OWL_E2E_OB_ORACLE_TEST_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	const table = "OWL_E2E_LV_BIG"
	dropTable(t, db, table)
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (id NUMBER PRIMARY KEY, big_long LONG, big_clob CLOB)"); err != nil {
		t.Skipf("create %s (LONG unsupported on this tenant?): %v", table, err)
	}
	t.Cleanup(func() { dropTable(t, db, table) })

	sizes := []int{24, 8*1024 + 7, 64*1024 + 13}
	withClob := true
	for i, n := range sizes {
		payload := deterministicPayload(n)
		var err error
		if withClob {
			_, err = db.ExecContext(ctx,
				"INSERT INTO "+table+" (id, big_long, big_clob) VALUES (:1, :2, :3)",
				int64(i+1), payload, payload)
			if err != nil {
				// 个别驱动/租户对 CLOB 绑定支持不完整：降级为 LONG-only，保住 LONG 覆盖
				withClob = false
				t.Logf("CLOB bind failed (%v); falling back to LONG-only for this and later rows", err)
			}
		}
		if !withClob {
			_, err = db.ExecContext(ctx,
				"INSERT INTO "+table+" (id, big_long) VALUES (:1, :2)",
				int64(i+1), payload)
		}
		if err != nil {
			t.Fatalf("insert row %d (%d bytes): %v", i+1, n, err)
		}
	}

	conn := pinnedConn(t, db)
	for round := 1; round <= 2; round++ {
		for i, n := range sizes {
			want := deterministicPayload(n)
			var gotClob, gotLong sql.NullString // Oracle 要求 LONG 列放 SELECT 列表末位
			err := conn.QueryRowContext(ctx,
				"SELECT big_clob, big_long FROM "+table+" WHERE id = :1", int64(i+1)).
				Scan(&gotClob, &gotLong)
			if err != nil {
				t.Fatalf("round %d read %d bytes: %v", round, n, err)
			}
			if !gotLong.Valid || gotLong.String != want {
				t.Fatalf("round %d LONG %d bytes mismatch: valid=%v gotLen=%d gotHash=%s wantHash=%s",
					round, n, gotLong.Valid, len(gotLong.String), hashOf(gotLong.String), hashOf(want))
			}
			if withClob && (!gotClob.Valid || gotClob.String != want) {
				t.Fatalf("round %d CLOB %d bytes mismatch: valid=%v gotLen=%d gotHash=%s wantHash=%s",
					round, n, gotClob.Valid, len(gotClob.String), hashOf(gotClob.String), hashOf(want))
			}
		}
	}
	t.Logf("OK: LONG(/CLOB) payloads of %v bytes read back twice on one conn, hashes match", sizes)
}

// ── P0.3 流中出错后连接恢复：行已发后失败 → END(ok:false)，同连接继续可用 ──

func TestE2E_AgentOBOracleMidStreamErrorRecovery(t *testing.T) {
	env := devEnvMap()
	db := openOracleE2E(t, env, "OWL_E2E_ORACLE_DSN", "OWL_E2E_OB_ORACLE_TEST_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const table = "OWL_E2E_LV_ERR"
	dropTable(t, db, table)
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (v VARCHAR2(16))"); err != nil {
		t.Skipf("create %s: %v", table, err)
	}
	t.Cleanup(func() { dropTable(t, db, table) })
	for _, s := range []string{"1", "2", "oops"} {
		if _, err := db.ExecContext(ctx, "INSERT INTO "+table+" (v) VALUES (:1)", s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	conn := pinnedConn(t, db)
	var streamErr error
	rows, err := conn.QueryContext(ctx, "SELECT TO_NUMBER(v) FROM "+table)
	if err != nil {
		streamErr = err // 错误在 header 之前浮出也算走通了失败路径，只要不挂起
	} else {
		for rows.Next() {
			var n sql.NullInt64
			if err := rows.Scan(&n); err != nil {
				streamErr = err
				break
			}
		}
		if streamErr == nil {
			streamErr = rows.Err()
		}
		rows.Close()
	}
	if streamErr == nil || errors.Is(streamErr, io.EOF) {
		t.Fatalf("expected mid-stream failure, got %v", streamErr)
	}
	t.Logf("mid-stream error as expected: %v", streamErr)

	// 关键：同一连接上后续查询恢复正常
	var v string
	if err := conn.QueryRowContext(ctx, "SELECT 'RECOVERED' FROM DUAL").Scan(&v); err != nil {
		t.Fatalf("same-conn query after mid-stream error: %v", err)
	}
	if v != "RECOVERED" {
		t.Fatalf("got %q", v)
	}
}

// ── P1.1 同连接高频顺序查询：本次竞态的窗口（正确性回归，不做吞吐基线）──

func TestE2E_AgentOBOracleSameConnLoop(t *testing.T) {
	env := devEnvMap()
	db := openOracleE2E(t, env, "OWL_E2E_ORACLE_DSN", "OWL_E2E_OB_ORACLE_MIGSRC_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	conn := pinnedConn(t, db)

	probe, err := findLongDefault(t, ctx, conn)
	if err != nil {
		t.Fatalf("probe data_default: %v", err)
	}
	base := probe.sample

	const total = 120
	for i := 0; i < total; i++ {
		if i%5 == 4 { // 每 5 次插一条 LONG 查询，压在 close→END 竞态窗口上
			got, err := queryDataDefault(ctx, conn, probe.col)
			if err != nil {
				t.Fatalf("iter %d LONG query on %s: %v", i, probe.col, err)
			}
			if got != base {
				t.Fatalf("iter %d LONG value drifted: %d vs %d bytes, hashes %s vs %s",
					i, len(got), len(base), hashOf(got), hashOf(base))
			}
			continue
		}
		var n int64
		if err := conn.QueryRowContext(ctx, "SELECT :1 FROM DUAL", int64(i)).Scan(&n); err != nil {
			t.Fatalf("iter %d plain query: %v", i, err)
		}
		if n != int64(i) {
			t.Fatalf("iter %d: got %d", i, n)
		}
	}
	t.Logf("OK: %d sequential queries on one conn (%d LONG interleaved)", total, total/5)
}

// ── P1：MySQL / PostgreSQL 的同连接顺序查询、错误前奏、空结果集、多连接 ──

type familySuite struct {
	bindQ  string // 带一个整数占位符（JDBC ? 风格）
	errQ   string // 语法错误查询（header 前失败）
	emptyQ string // 恒空结果集
	okQ    string // 无 FROM 的普通查询
}

func sameConnSuite(t *testing.T, db *sql.DB, s familySuite) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// 单连接 60 条顺序查询（完成帧时序的窗口）
	conn := pinnedConn(t, db)
	for i := 0; i < 60; i++ {
		var n int64
		if err := conn.QueryRowContext(ctx, s.bindQ, int64(i)).Scan(&n); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if n != int64(i) {
			t.Fatalf("iter %d: got %d", i, n)
		}
	}
	t.Log("OK: 60 sequential queries on one conn")

	// 错误前奏：语法错误 → RESPONSE(ok:false)，后续查询正常
	if _, err := conn.QueryContext(ctx, s.errQ); err == nil {
		t.Fatal("expected syntax error")
	}
	var v string
	if err := conn.QueryRowContext(ctx, s.okQ).Scan(&v); err != nil {
		t.Fatalf("query after syntax error: %v", err)
	}
	if v != "AFTER_ERR" {
		t.Fatalf("got %q", v)
	}
	t.Log("OK: query after syntax error")

	// 空结果集：END(rows=0) 正常返回
	rows, err := conn.QueryContext(ctx, s.emptyQ)
	if err != nil {
		t.Fatalf("empty query: %v", err)
	}
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if count != 0 {
		t.Fatalf("empty query returned %d rows", count)
	}
	t.Log("OK: empty result set")

	// 双连接交错：互不串扰
	c2 := pinnedConn(t, db)
	for i := 0; i < 6; i++ {
		var a, b int64
		if err := conn.QueryRowContext(ctx, s.bindQ, int64(i)).Scan(&a); err != nil {
			t.Fatalf("conn1 iter %d: %v", i, err)
		}
		if err := c2.QueryRowContext(ctx, s.bindQ, int64(1000+i)).Scan(&b); err != nil {
			t.Fatalf("conn2 iter %d: %v", i, err)
		}
		if a != int64(i) || b != int64(1000+i) {
			t.Fatalf("conn bleed at iter %d: conn1=%d conn2=%d", i, a, b)
		}
	}
	t.Log("OK: two conns interleaved without bleed")
}

func TestE2E_AgentMySQLSameConn(t *testing.T) {
	env := devEnvMap()
	db := openFamilyE2E(t, env, "mysql", "OWL_E2E_MYSQL_DSN")
	sameConnSuite(t, db, familySuite{
		bindQ:  "SELECT ?",
		errQ:   "SELEC 1",
		emptyQ: "SELECT 1 FROM DUAL WHERE 1=0",
		okQ:    "SELECT 'AFTER_ERR'",
	})
}

func TestE2E_AgentPGSameConn(t *testing.T) {
	env := devEnvMap()
	db := openFamilyE2E(t, env, "postgres", "OWL_E2E_PG_DSN")
	sameConnSuite(t, db, familySuite{
		bindQ:  "SELECT ?",
		errQ:   "SELEC 1",
		emptyQ: "SELECT 1 WHERE FALSE",
		okQ:    "SELECT 'AFTER_ERR'",
	})
}
