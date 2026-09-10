package owl.agent;

import java.util.LinkedHashMap;
import java.util.Map;

/** 极简 JSON 工具：扁平对象解析（string-aware，值内逗号/冒号不影响）与字符串转义。 */
public final class Json {
    public static Map<String, String> parseObject(String s) {
        Map<String, String> m = new LinkedHashMap<>();
        int i = 0, n = s.length();
        while (i < n) {
            char c = s.charAt(i);
            if (c == '{' || c == '}' || c == ',' || Character.isWhitespace(c)) { i++; continue; }
            if (c != '"') { i++; continue; }
            int[] keyEnd = new int[1];
            String key = parseString(s, i, keyEnd);
            i = keyEnd[0];
            while (i < n && (Character.isWhitespace(s.charAt(i)) || s.charAt(i) == ':')) i++;
            if (i >= n) break;
            String value;
            if (s.charAt(i) == '"') {
                int[] valEnd = new int[1];
                value = parseString(s, i, valEnd);
                i = valEnd[0];
            } else {
                int j = i;
                while (j < n && s.charAt(j) != ',' && s.charAt(j) != '}') j++;
                value = s.substring(i, j).trim();
                i = j;
            }
            m.put(key, value);
        }
        return m;
    }

    /** 从 s[start]=='"' 起解析 JSON 字符串，返回解码值；end[0] = 结束引号之后的位置。 */
    private static String parseString(String s, int start, int[] end) {
        StringBuilder sb = new StringBuilder();
        int i = start + 1, n = s.length();
        while (i < n) {
            char c = s.charAt(i);
            if (c == '"') { i++; break; }
            if (c == '\\' && i + 1 < n) {
                char e = s.charAt(i + 1);
                switch (e) {
                    case '"': sb.append('"'); i += 2; continue;
                    case '\\': sb.append('\\'); i += 2; continue;
                    case '/': sb.append('/'); i += 2; continue;
                    case 'b': sb.append('\b'); i += 2; continue;
                    case 'f': sb.append('\f'); i += 2; continue;
                    case 'n': sb.append('\n'); i += 2; continue;
                    case 'r': sb.append('\r'); i += 2; continue;
                    case 't': sb.append('\t'); i += 2; continue;
                    case 'u':
                        if (i + 5 < n) {
                            sb.append((char) Integer.parseInt(s.substring(i + 2, i + 6), 16));
                            i += 6;
                            continue;
                        }
                        i++;
                        continue;
                    default:
                        sb.append(e);
                        i += 2;
                        continue;
                }
            }
            sb.append(c);
            i++;
        }
        end[0] = i;
        return sb.toString();
    }

    public static String escape(String s) {
        StringBuilder sb = new StringBuilder(s.length() + 8);
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"': sb.append("\\\""); break;
                case '\\': sb.append("\\\\"); break;
                case '\b': sb.append("\\b"); break;
                case '\f': sb.append("\\f"); break;
                case '\n': sb.append("\\n"); break;
                case '\r': sb.append("\\r"); break;
                case '\t': sb.append("\\t"); break;
                default:
                    if (c < 0x20) sb.append(String.format("\\u%04x", (int) c));
                    else sb.append(c);
            }
        }
        return sb.toString();
    }
}
