package owljdbc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterSpec_AndBuildConfig(t *testing.T) {
	dir := t.TempDir()
	for _, j := range []string{"ojdbc11.jar", "owl-agent.jar"} {
		if err := os.WriteFile(filepath.Join(dir, j), []byte("PK"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := RegisterSpec("specora", ProfileSpec{
		DriverClass: "oracle.jdbc.OracleDriver",
		JarGlobs:    []string{"ojdbc*.jar"},
		Family:      "oracle",
		URLTemplate: "jdbc:oracle:thin:@//{host}:{port}/{database}",
	})
	if err != nil {
		t.Fatalf("RegisterSpec: %v", err)
	}

	if !HasProfile("SpecORA ") { // 大小写与空白归一
		t.Fatal("HasProfile(specora) = false after register")
	}
	if got := ProfileFamily("specora"); got != "oracle" {
		t.Fatalf("ProfileFamily = %q, want oracle", got)
	}

	cfg, err := BuildConfig("specora",
		Endpoint{Host: "h1", Port: "1521", User: "u", Password: "p", Database: "SVC"},
		"oracle://u:p@h1:1521/SVC", dir, "", "")
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if cfg.URL != "jdbc:oracle:thin:@//h1:1521/SVC" {
		t.Fatalf("URL = %q", cfg.URL)
	}
	if cfg.User != "u" || cfg.Password != "p" {
		t.Fatalf("credentials must ride the connection params, got %q/%q", cfg.User, cfg.Password)
	}
	if cfg.Family != "oracle" {
		t.Fatalf("Family = %q", cfg.Family)
	}
	if cfg.DriverClass != "oracle.jdbc.OracleDriver" {
		t.Fatalf("DriverClass = %q", cfg.DriverClass)
	}
}

func TestRegisterSpec_OverridesBuiltIn(t *testing.T) {
	orig, ok := ProfileFor("dm")
	if !ok {
		t.Fatal("built-in dm profile missing")
	}
	defer func() {
		specMu.Lock()
		profiles["dm"] = orig
		specMu.Unlock()
	}()

	err := RegisterSpec("DM", ProfileSpec{
		DriverClass: "dm.jdbc.driver.DmDriver",
		JarGlobs:    []string{"DmJdbcDriver*.jar"},
		Family:      "oracle",
		URLTemplate: "jdbc:dm://{host}:{port}",
	})
	if err != nil {
		t.Fatalf("RegisterSpec override: %v", err)
	}
	p, _ := ProfileFor("dm")
	if p.URLFromDSN { // 内置 dm 是 URLFromDSN;覆盖后应为模板构建
		t.Fatal("override did not take effect")
	}
}

func TestRegisterSpec_Validation(t *testing.T) {
	cases := []struct {
		name string
		spec ProfileSpec
		want string
	}{
		{"bad family", ProfileSpec{DriverClass: "x", JarGlobs: []string{"a.jar"}, Family: "mariadb", URLTemplate: "jdbc:// {host}"},
			"family must be"},
		{"no driver", ProfileSpec{JarGlobs: []string{"a.jar"}, Family: "oracle", URLTemplate: "jdbc://{host}"},
			"driver_class is required"},
		{"no globs", ProfileSpec{DriverClass: "x", Family: "oracle", URLTemplate: "jdbc://{host}"},
			"jar glob is required"},
		{"template missing host", ProfileSpec{DriverClass: "x", JarGlobs: []string{"a.jar"}, Family: "oracle",
			URLTemplate: "jdbc:x:{port}"},
			"must contain {host}"},
		{"unknown placeholder", ProfileSpec{DriverClass: "x", JarGlobs: []string{"a.jar"}, Family: "oracle",
			URLTemplate: "jdbc:x://{host}/{user}"},
			"unsupported placeholder {user}"},
		{"unbalanced brace", ProfileSpec{DriverClass: "x", JarGlobs: []string{"a.jar"}, Family: "oracle",
			URLTemplate: "jdbc:x://{host"},
			"unbalanced"},
	}
	for _, tc := range cases {
		err := RegisterSpec("tmp_fail_"+tc.name, tc.spec)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want contains %q", tc.name, err, tc.want)
		}
		if HasProfile("tmp_fail_" + tc.name) {
			t.Errorf("%s: failed spec must not register", tc.name)
		}
	}
}

func TestRegisterSpec_DSNRaw(t *testing.T) {
	dir := t.TempDir()
	for _, j := range []string{"ex-1.jar", "owl-agent.jar"} {
		if err := os.WriteFile(filepath.Join(dir, j), []byte("PK"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := RegisterSpec("specraw", ProfileSpec{
		DriverClass: "com.example.Driver",
		JarGlobs:    []string{"ex-*.jar"},
		Family:      "postgres",
		DSNRaw:      true,
		DSNSyntax:   "kv",
	})
	if err != nil {
		t.Fatalf("RegisterSpec dsn_raw: %v", err)
	}
	cfg, err := BuildConfig("specraw", Endpoint{}, "jdbc:example://whatever;x=1", dir, "", "")
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if cfg.URL != "jdbc:example://whatever;x=1" {
		t.Fatalf("raw DSN must pass through, got %q", cfg.URL)
	}
	if cfg.User != "" || cfg.Password != "" {
		t.Fatal("raw-URL profiles must not carry separate credentials")
	}
}

func TestRegisterSpecs_AllOrNothing(t *testing.T) {
	err := RegisterSpecs(map[string]ProfileSpec{
		"specbatch_ok":  {DriverClass: "x", JarGlobs: []string{"a.jar"}, Family: "mysql", URLTemplate: "jdbc:x://{host}:{port}/{database}"},
		"specbatch_bad": {DriverClass: "x", JarGlobs: []string{"a.jar"}, Family: "bogus", URLTemplate: "jdbc:x://{host}"},
	})
	if err == nil {
		t.Fatal("RegisterSpecs must fail on invalid entry")
	}
	if HasProfile("specbatch_ok") {
		t.Fatal("all-or-nothing violated: valid entry registered despite batch failure")
	}
}
