package owljdbc

import (
	"strings"
)

// adaptSQL 把 Go 侧 SQL 适配成 JDBC 可执行的形态。postgres 族的 $N 是
// 服务端 PREPARE 语义,PG JDBC 的 PreparedStatement 只认 "?"——经 owljdbc
// 通道时占位符统一在连接层改写(与 sidecar 对 oracle :N 的 BindRewriter
// 对偶)。字面量(单引号字符串/双引号标识符/注释)中的 $N 原样保留。
// 其它 family 原样返回。
func (c *Conn) adaptSQL(query string) string {
	if c.cfg.Family != "postgres" {
		return query
	}
	return rewriteDollarPlaceholders(query)
}

// rewriteDollarPlaceholders 把 SQL 中的 $N 占位符改写为 ?,跳过
// 单引号字符串(” 转义)、双引号标识符、行注释与块注释、E” 转义串。
func rewriteDollarPlaceholders(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))
	i, n := 0, len(sql)
	for i < n {
		ch := sql[i]
		switch {
		case ch == '\'': // 单引号字符串(E'...' 的 E 已在前一循环作为普通字符输出,'' 转义)
			j := i + 1
			for j < n {
				if sql[j] == '\'' {
					if j+1 < n && sql[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			b.WriteString(sql[i:min(j+1, n)])
			i = min(j+1, n)
		case ch == '"': // 双引号标识符
			j := i + 1
			for j < n && sql[j] != '"' {
				j++
			}
			b.WriteString(sql[i:min(j+1, n)])
			i = min(j+1, n)
		case ch == '-' && i+1 < n && sql[i+1] == '-': // 行注释
			j := i
			for j < n && sql[j] != '\n' {
				j++
			}
			b.WriteString(sql[i:j])
			i = j
		case ch == '/' && i+1 < n && sql[i+1] == '*': // 块注释
			j := strings.Index(sql[i+2:], "*/")
			end := n
			if j >= 0 {
				end = i + 2 + j + 2
			}
			b.WriteString(sql[i:end])
			i = end
		case ch == '$' && i+1 < n && sql[i+1] >= '0' && sql[i+1] <= '9':
			j := i + 1
			for j < n && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			b.WriteByte('?')
			i = j
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
