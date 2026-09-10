package owljdbc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileURLs(t *testing.T) {
	dir := t.TempDir()
	for _, jar := range []string{
		"owl-agent.jar", "mysql-connector-j-8.0.33.jar", "oceanbase-client-2.4.1.jar",
		"postgresql-42.7.13.jar", "ojdbc11.jar", "DmJdbcDriver18.jar",
	} {
		if err := os.WriteFile(filepath.Join(dir, jar), []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := Endpoint{Host: "h1", Port: "2881", User: "u", Password: "p", Database: "app"}
	cases := []struct {
		dbType  string
		wantURL string
		family  string
	}{
		{"mysql", "jdbc:mysql://h1:2881/app?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8", "mysql"},
		{"oceanbase-mysql", "jdbc:oceanbase://h1:2881/app?useSSL=false&characterEncoding=UTF-8", "mysql"},
		{"oceanbase-oracle", "jdbc:oceanbase://h1:2881?useSSL=false", "oracle"},
		{"postgres", "jdbc:postgresql://h1:2881/app?stringtype=unspecified", "postgres"},
		{"dm", "jdbc:dm://h1:2881", "oracle"},
	}
	for _, tc := range cases {
		t.Run(tc.dbType, func(t *testing.T) {
			cfg, err := BuildConfig(tc.dbType, e, "", dir, filepath.Join(dir, "owl-agent.jar"), "")
			if err != nil {
				t.Fatalf("BuildConfig: %v", err)
			}
			if cfg.URL != tc.wantURL {
				t.Fatalf("url = %q, want %q", cfg.URL, tc.wantURL)
			}
			if cfg.Family != tc.family {
				t.Fatalf("family = %q, want %q", cfg.Family, tc.family)
			}
		})
	}
}

func TestHasProfile(t *testing.T) {
	if !HasProfile("OCEANBASE-MYSQL") {
		t.Fatal("normalized case must match")
	}
	if HasProfile("yashandb") {
		t.Fatal("yashandb not registered")
	}
}

func TestResolveDriverJarsAnyGlobWins(t *testing.T) {
	dir := t.TempDir()
	if err := osWriteFile(dir, "DmJdbcDriver18.jar"); err != nil {
		t.Fatal(err)
	}
	jars, err := ResolveDriverJars([]string{dir}, []string{"DmJdbcDriver*.jar", "dm-jdbc-*.jar"})
	if err != nil {
		t.Fatalf("any-glob must win: %v", err)
	}
	if !strings.HasSuffix(jars[0], "DmJdbcDriver18.jar") {
		t.Fatalf("jar = %q", jars[0])
	}
	if _, err := ResolveDriverJars([]string{dir}, []string{"nope-*.jar"}); err == nil {
		t.Fatal("missing jar must error")
	}
}

func osWriteFile(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte("jar"), 0o644)
}
