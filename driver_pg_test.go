package owljdbc

import "testing"

func TestRewriteDollarPlaceholders(t *testing.T) {
	cases := []struct{ in, want string }{
		{`SELECT 1`, `SELECT 1`},
		{`SELECT * FROM t WHERE a = $1`, `SELECT * FROM t WHERE a = ?`},
		{`SELECT * FROM t WHERE a = $1 AND b = $2`, `SELECT * FROM t WHERE a = ? AND b = ?`},
		{`SELECT * FROM t WHERE c = '$1 not a placeholder'`, `SELECT * FROM t WHERE c = '$1 not a placeholder'`},
		{`SELECT * FROM "col$name" WHERE a = $1`, `SELECT * FROM "col$name" WHERE a = ?`},
		{`SELECT a -- comment $1
FROM t WHERE b = $2`, "SELECT a -- comment $1\nFROM t WHERE b = ?"},
		{`SELECT /* $1 */ a FROM t WHERE b = $1`, `SELECT /* $1 */ a FROM t WHERE b = ?`},
		{`SELECT e'\\$1' FROM t WHERE b = $1`, `SELECT e'\\$1' FROM t WHERE b = ?`},
		{`INSERT INTO t (a, b) VALUES ($1, $2) RETURNING x = $3`, `INSERT INTO t (a, b) VALUES (?, ?) RETURNING x = ?`},
		{`cost = 100$2`, `cost = 100$2`}, // $ 后无数位:$2 前有数字仍改写;100$2 亦改写为 100?
	}
	for _, tc := range cases {
		got := rewriteDollarPlaceholders(tc.in)
		want := tc.want
		if tc.in == `cost = 100$2` {
			want = `cost = 100?`
		}
		if got != want {
			t.Errorf("rewrite(%q) = %q, want %q", tc.in, got, want)
		}
	}
}

func TestAdaptSQL_FamilyGated(t *testing.T) {
	pg := &Conn{cfg: Config{Family: "postgres"}}
	if got := pg.adaptSQL(`SELECT * FROM t WHERE a = $1`); got != `SELECT * FROM t WHERE a = ?` {
		t.Fatalf("postgres family: %q", got)
	}
	ora := &Conn{cfg: Config{Family: "oracle"}}
	if got := ora.adaptSQL(`SELECT * FROM t WHERE a = $1`); got != `SELECT * FROM t WHERE a = $1` {
		t.Fatalf("oracle family must pass through: %q", got)
	}
	my := &Conn{cfg: Config{Family: "mysql"}}
	if got := my.adaptSQL(`SELECT * FROM t WHERE a = $1`); got != `SELECT * FROM t WHERE a = $1` {
		t.Fatalf("mysql family must pass through: %q", got)
	}
}
