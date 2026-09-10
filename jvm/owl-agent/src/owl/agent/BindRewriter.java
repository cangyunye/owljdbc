package owl.agent;

/** 把 Oracle 风格 :N 绑定占位符改写为 JDBC "?"；跳过字符串字面量、双引号标识符与注释。 */
public final class BindRewriter {
    public static String rewrite(String sql) {
        StringBuilder out = new StringBuilder(sql.length());
        int i = 0, n = sql.length();
        while (i < n) {
            char c = sql.charAt(i);
            if (c == '\'') {                       // 单引号字符串（'' 转义）
                int j = i + 1;
                while (j < n) {
                    if (sql.charAt(j) == '\'') {
                        if (j + 1 < n && sql.charAt(j + 1) == '\'') { j += 2; continue; }
                        break;
                    }
                    j++;
                }
                out.append(sql, i, Math.min(j + 1, n));
                i = Math.min(j + 1, n);
                continue;
            }
            if (c == '"') {                        // 双引号标识符
                int j = i + 1;
                while (j < n && sql.charAt(j) != '"') j++;
                out.append(sql, i, Math.min(j + 1, n));
                i = Math.min(j + 1, n);
                continue;
            }
            if (c == '-' && i + 1 < n && sql.charAt(i + 1) == '-') {   // 行注释
                int j = i;
                while (j < n && sql.charAt(j) != '\n') j++;
                out.append(sql, i, j);
                i = j;
                continue;
            }
            if (c == '/' && i + 1 < n && sql.charAt(i + 1) == '*') {   // 块注释
                int j = sql.indexOf("*/", i + 2);
                int end = j < 0 ? n : j + 2;
                out.append(sql, i, end);
                i = end;
                continue;
            }
            if (c == ':' && i + 1 < n && Character.isDigit(sql.charAt(i + 1))) {
                boolean prevColon = i > 0 && sql.charAt(i - 1) == ':';   // ::（如 PG cast 无关，但防御）
                if (!prevColon) {                                        // := 赋值：下一个字符是数字才可能是 :N，'=' 不匹配
                    int j = i + 1;
                    while (j < n && Character.isDigit(sql.charAt(j))) j++;
                    out.append('?');
                    i = j;
                    continue;
                }
            }
            out.append(c);
            i++;
        }
        return out.toString();
    }
}
