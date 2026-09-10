package owljdbc

import "encoding/json"

// Config 描述一个 JDBC agent 连接（序列化为 database/sql DSN）。
type Config struct {
	DriverClass string   `json:"driverClass"`
	URL         string   `json:"url"`
	User        string   `json:"user"`
	Password    string   `json:"password"`
	Family      string   `json:"family"` // "mysql" | "oracle"
	Classpath   []string `json:"classpath"`
	JavaHome    string   `json:"javaHome,omitempty"`
	AgentJar    string   `json:"agentJar"`
}

func EncodeDSN(cfg Config) string {
	b, _ := json.Marshal(cfg)
	return string(b)
}

func DecodeDSN(dsn string) (Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(dsn), &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
