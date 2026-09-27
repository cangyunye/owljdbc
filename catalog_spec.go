package owljdbc

import (
	"fmt"
	"strings"
	"sync"
)

// ProfileSpec 是 Profile 的可序列化声明,供宿主从配置文件注册新数据库类型,
// 免改代码接入 Oracle/MySQL/PostgreSQL 三大族兼容的 JDBC 驱动。内置 catalog
// 是基线,RegisterSpec 注册的同名类型覆盖内置条目。
//
// 字段语义:
//   - DriverClass: JDBC 驱动类名(sidecar Class.forName)。
//   - JarGlobs:    驱动 jar 的候选文件名 glob(任一匹配即可)。
//   - Family:      连接语义族 oracle | mysql | postgres——决定 SQL 方言假设、
//     占位符改写与宿主的字典族归一;必须是三族之一。
//   - URLTemplate: JDBC URL 模板,支持 {host} {port} {database} 占位符。
//     凭据不走模板(owljdbc 经 DriverManager 参数传递,避免密码进 URL 日志)。
//   - DSNRaw:      为 true 时忽略 URLTemplate,宿主 DSN 本身就是完整 JDBC URL,
//     原样透传(如 TimesTen 手工 client DSN)。
//   - DSNSyntax:   宿主侧 DSN 语法提示(url | pg-kv | mysql-tcp | kv),owljdbc
//     不消费该字段——DSN 解析发生在宿主(dsnfields),此声明随 spec 走便于
//     配置自描述。
type ProfileSpec struct {
	DriverClass string   `yaml:"driver_class" json:"driver_class"`
	JarGlobs    []string `yaml:"jar_globs" json:"jar_globs"`
	Family      string   `yaml:"family" json:"family"`
	URLTemplate string   `yaml:"url_template" json:"url_template"`
	DSNRaw      bool     `yaml:"dsn_raw" json:"dsn_raw"`
	DSNSyntax   string   `yaml:"dsn_syntax" json:"dsn_syntax"`
}

var (
	specMu sync.RWMutex
	// errSpec 是 RegisterSpec 校验失败的可判别错误。
	errSpec = fmt.Errorf("owljdbc: invalid profile spec")
)

// RegisterSpec 注册(或覆盖)一个数据库类型的接入 profile。校验失败返回
// 错误且不产生任何变更;同名类型覆盖内置/先前注册。
func RegisterSpec(dbType string, spec ProfileSpec) error {
	prof, err := spec.validate(dbType)
	if err != nil {
		return err
	}
	specMu.Lock()
	defer specMu.Unlock()
	profiles[strings.ToLower(strings.TrimSpace(dbType))] = prof
	return nil
}

// RegisterSpecs 批量注册;任一条目校验失败即整体失败且不注册任何条目
// (先全量校验再落盘,避免半套 profile 生效)。
func RegisterSpecs(specs map[string]ProfileSpec) error {
	built := make(map[string]Profile, len(specs))
	for name, spec := range specs {
		prof, err := spec.validate(name)
		if err != nil {
			return err
		}
		built[strings.ToLower(strings.TrimSpace(name))] = prof
	}
	specMu.Lock()
	defer specMu.Unlock()
	for t, prof := range built {
		profiles[t] = prof
	}
	return nil
}

// ProfileFamily 返回该类型的连接语义族;未注册返回空串。宿主用它做
// 字典族/占位符族归一(先查外部注册,再落各自的内置表)。
func ProfileFamily(dbType string) string {
	specMu.RLock()
	defer specMu.RUnlock()
	if p, ok := profiles[strings.ToLower(strings.TrimSpace(dbType))]; ok {
		return p.Family
	}
	return ""
}

// validate 校验并编译 spec 为 Profile;不触碰全局注册表。
func (s ProfileSpec) validate(dbType string) (Profile, error) {
	t := strings.ToLower(strings.TrimSpace(dbType))
	if t == "" {
		return Profile{}, fmt.Errorf("%w: empty database type", errSpec)
	}
	s.Family = strings.ToLower(strings.TrimSpace(s.Family))
	switch s.Family {
	case "oracle", "mysql", "postgres":
	default:
		return Profile{}, fmt.Errorf("%w: %s: family must be oracle, mysql, or postgres", errSpec, t)
	}
	if strings.TrimSpace(s.DriverClass) == "" {
		return Profile{}, fmt.Errorf("%w: %s: driver_class is required", errSpec, t)
	}
	if len(s.JarGlobs) == 0 {
		return Profile{}, fmt.Errorf("%w: %s: at least one jar glob is required", errSpec, t)
	}
	for _, g := range s.JarGlobs {
		if strings.TrimSpace(g) == "" {
			return Profile{}, fmt.Errorf("%w: %s: empty jar glob", errSpec, t)
		}
	}
	prof := Profile{
		DriverClass: strings.TrimSpace(s.DriverClass),
		Family:      s.Family,
		JarGlobs:    append([]string(nil), s.JarGlobs...),
		URLFromDSN:  s.DSNRaw,
	}
	if !s.DSNRaw {
		if err := validateURLTemplate(s.URLTemplate); err != nil {
			return Profile{}, fmt.Errorf("%w: %s: %v", errSpec, t, err)
		}
		prof.BuildURL = templateURLFunc(s.URLTemplate)
	}
	return prof, nil
}

// validateURLTemplate 校验模板只含字面文本与 {host}/{port}/{database} 占位符。
func validateURLTemplate(tpl string) error {
	if !strings.Contains(tpl, "{host}") {
		return fmt.Errorf("url_template must contain {host}")
	}
	for i := 0; i < len(tpl); i++ {
		if tpl[i] != '{' {
			continue
		}
		end := strings.IndexByte(tpl[i:], '}')
		if end < 0 {
			return fmt.Errorf("unbalanced '{' at offset %d", i)
		}
		name := tpl[i+1 : i+end]
		switch name {
		case "host", "port", "database":
		default:
			return fmt.Errorf("unsupported placeholder {%s} (want host/port/database)", name)
		}
		i += end
	}
	return nil
}

// templateURLFunc 把模板编译为 Profile.BuildURL。
func templateURLFunc(tpl string) func(Endpoint) (string, error) {
	return func(e Endpoint) (string, error) {
		if strings.TrimSpace(e.Host) == "" {
			return "", fmt.Errorf("endpoint host is required")
		}
		r := strings.NewReplacer(
			"{host}", e.Host,
			"{port}", e.Port,
			"{database}", e.Database,
		)
		return r.Replace(tpl), nil
	}
}
