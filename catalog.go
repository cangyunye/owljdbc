package owljdbc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Endpoint 是已解析的连接要素。宿主侧用各自的 DSN 语法解析出这些字段
// （Go 生态可复用项目内的 DSN 解析器），再交给 BuildConfig 组装 owljdbc 连接。
type Endpoint struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

// HostPort 渲染 host 或 host:port（端口缺失时省略冒号段）。
func (e Endpoint) HostPort() string {
	if e.Port == "" {
		return e.Host
	}
	return e.Host + ":" + e.Port
}

// Profile 描述一类数据库如何经 owljdbc 通道接入：JDBC 驱动类、连接 family、
// 驱动 jar 的候选文件名（vendor 命名随版本变化，故为多 glob 任一匹配）与
// JDBC URL 构造规则。URLFromDSN 为 true 时表示调用方的 DSN 本身就是完整
// JDBC URL，原样使用。
type Profile struct {
	DriverClass string
	Family      string // mysql | oracle | postgres
	JarGlobs    []string
	URLFromDSN  bool
	BuildURL    func(e Endpoint) (string, error)
}

// 目录内容镜像阶段一架构文档 §11 的登记矩阵；标注"登记未测"的 profile
// 首次接入真实数据库后应回填实测结论。
var profiles = map[string]Profile{
	// 单 jar 双租户（已实测）：同一 driverClass 覆盖 MySQL 与 Oracle 兼容租户。
	"oceanbase-mysql": {
		DriverClass: "com.oceanbase.jdbc.Driver",
		Family:      "mysql",
		JarGlobs:    []string{"oceanbase-client-*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			return fmt.Sprintf("jdbc:oceanbase://%s/%s?useSSL=false&characterEncoding=UTF-8", e.HostPort(), e.Database), nil
		},
	},
	"oceanbase-oracle": {
		DriverClass: "com.oceanbase.jdbc.Driver",
		Family:      "oracle",
		JarGlobs:    []string{"oceanbase-client-*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			return fmt.Sprintf("jdbc:oceanbase://%s?useSSL=false", e.HostPort()), nil
		},
	},
	"mysql": {
		DriverClass: "com.mysql.cj.jdbc.Driver",
		Family:      "mysql",
		JarGlobs:    []string{"mysql-connector-j-*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			return fmt.Sprintf("jdbc:mysql://%s/%s?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8", e.HostPort(), e.Database), nil
		},
	},
	"postgres": {
		DriverClass: "org.postgresql.Driver",
		Family:      "postgres",
		JarGlobs:    []string{"postgresql-*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			// stringtype=unspecified：字符串参数以 unspecified 类型发给服务端，
			// 由目标列推断类型——与 native pq 的 unknown-oid 行为一致（迁移
			// 场景的宽松隐式转换正是对拍等价所需要的）。
			return fmt.Sprintf("jdbc:postgresql://%s/%s?stringtype=unspecified", e.HostPort(), e.Database), nil
		},
	},
	// 登记未测：driverClass/URL 来自架构文档 §11 与厂商公开文档。
	"oracle": {
		DriverClass: "oracle.jdbc.OracleDriver",
		Family:      "oracle",
		JarGlobs:    []string{"ojdbc*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			return fmt.Sprintf("jdbc:oracle:thin:@//%s/%s", e.HostPort(), e.Database), nil
		},
	},
	"goldendb-mysql": {
		DriverClass: "com.mysql.cj.jdbc.Driver",
		Family:      "mysql",
		JarGlobs:    []string{"mysql-connector-j-*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			return fmt.Sprintf("jdbc:mysql://%s/%s?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8", e.HostPort(), e.Database), nil
		},
	},
	// goldendb-oracle：登记未测——GoldenDB Oracle 兼容模式的 JDBC 驱动类
	// 待厂商最终确认，先按 Oracle 兼容系登记，首次接入时实测修订。
	"goldendb-oracle": {
		DriverClass: "oracle.jdbc.OracleDriver",
		Family:      "oracle",
		JarGlobs:    []string{"goldendb-jdbc-*.jar", "ojdbc*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			return fmt.Sprintf("jdbc:oracle:thin:@//%s/%s", e.HostPort(), e.Database), nil
		},
	},
	"dm": {
		DriverClass: "dm.jdbc.driver.DmDriver",
		Family:      "oracle",
		JarGlobs:    []string{"DmJdbcDriver*.jar", "dm-jdbc-*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			return fmt.Sprintf("jdbc:dm://%s", e.HostPort()), nil
		},
	},
	"kingbase": {
		DriverClass: "com.kingbase8.Driver",
		Family:      "postgres",
		JarGlobs:    []string{"kingbase8-*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			return fmt.Sprintf("jdbc:kingbase8://%s/%s", e.HostPort(), e.Database), nil
		},
	},
	// openGauss JDBC 6.x（驱动类 org.opengauss.Driver；JarGlobs 覆盖官方
	// opengauss-jdbc-*.jar 命名）。兼容模式（-mysql/-oracle）只影响方言，
	// wire 与 JDBC 路径相同。
	"opengaussdb": {
		DriverClass: "org.opengauss.Driver",
		Family:      "postgres",
		JarGlobs:    []string{"opengauss-jdbc-*.jar"},
		BuildURL: func(e Endpoint) (string, error) {
			return fmt.Sprintf("jdbc:opengauss://%s/%s", e.HostPort(), e.Database), nil
		},
	},
	"timesten": {
		DriverClass: "com.timesten.jdbc.TimesTenDriver",
		Family:      "oracle",
		URLFromDSN:  true,
		JarGlobs:    []string{"ttjdbc*.jar"},
	},
}

// ProfileFor 返回数据库类型对应的接入 profile；dbType 需为调用方归一化后
// 的类型（模块不感知各项目的类型别名归一化规则）。
func ProfileFor(dbType string) (Profile, bool) {
	p, ok := profiles[strings.ToLower(strings.TrimSpace(dbType))]
	return p, ok
}

// HasProfile 报告该类型是否可经 owljdbc 通道接入。
func HasProfile(dbType string) bool {
	_, ok := profiles[strings.ToLower(strings.TrimSpace(dbType))]
	return ok
}

// BuildConfig 按 profile 组装 owljdbc 连接配置：解析 URL（或原样透传 DSN）、
// 解析 agent jar 与驱动 jar 的真实路径。
func BuildConfig(dbType string, e Endpoint, dsn, jarsDir, agentJar, javaHome string) (Config, error) {
	prof, ok := ProfileFor(dbType)
	if !ok {
		return Config{}, fmt.Errorf("owljdbc: no catalog profile for database type %q", dbType)
	}
	urlStr := strings.TrimSpace(dsn)
	if !prof.URLFromDSN {
		if e.Host == "" {
			return Config{}, fmt.Errorf("owljdbc: type %q requires a dsn with a host", dbType)
		}
		var err error
		if urlStr, err = prof.BuildURL(e); err != nil {
			return Config{}, fmt.Errorf("owljdbc: build jdbc url: %w", err)
		}
	}
	if urlStr == "" {
		return Config{}, fmt.Errorf("owljdbc: type %q requires a non-empty dsn", dbType)
	}

	dirs := JarSearchDirs(jarsDir)
	driverJars, err := ResolveDriverJars(dirs, prof.JarGlobs)
	if err != nil {
		return Config{}, err
	}
	jar, err := ResolveAgentJar(dirs, agentJar)
	if err != nil {
		return Config{}, err
	}

	// URLFromDSN profile 的凭证随 JDBC URL 自带（如 TimesTen uid/pwd）。
	user, pass := e.User, e.Password
	if prof.URLFromDSN {
		user, pass = "", ""
	}

	return Config{
		DriverClass: prof.DriverClass,
		URL:         urlStr,
		User:        user,
		Password:    pass,
		Family:      prof.Family,
		Classpath:   driverJars,
		JavaHome:    javaHome,
		AgentJar:    jar,
	}, nil
}

// JarSearchDirs 返回 jar 搜索目录序列：显式配置目录优先，其次工作目录。
func JarSearchDirs(configured string) []string {
	dirs := []string{}
	if configured != "" {
		dirs = append(dirs, configured)
	}
	return append(dirs, ".")
}

// ResolveDriverJars 在候选 glob 中定位驱动 jar（任一匹配即可，首中为准）。
func ResolveDriverJars(dirs []string, globs []string) ([]string, error) {
	for _, pattern := range globs {
		found, err := findFile(dirs, pattern)
		if err == nil {
			return []string{found}, nil
		}
	}
	return nil, fmt.Errorf("owljdbc: no jar matching %s in %v; download the driver jar or set jars_dir / agent_jar", strings.Join(globs, " | "), dirs)
}

// ResolveAgentJar 解析 owl-agent.jar：显式路径直接校验存在；否则按通配符搜索。
func ResolveAgentJar(dirs []string, configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("owljdbc: agent jar %s: %w", configured, err)
		}
		return configured, nil
	}
	return findFile(dirs, "owl-agent*.jar")
}

func findFile(dirs []string, pattern string) (string, error) {
	for _, dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		sort.Strings(matches)
		if len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", fmt.Errorf("no jar matching %s in %v", pattern, dirs)
}
