package owl.agent.test;

import owl.agent.Json;

import java.util.Map;

public class JsonTest {
    static int fails = 0;
    static void eq(String name, String got, String want) {
        if (!want.equals(got)) {
            System.out.println("FAIL " + name + ": got [" + got + "] want [" + want + "]");
            fails++;
        }
    }
    public static void main(String[] a) {
        // sql 字段含逗号——split-on-comma 解析器的致命场景
        Map<String, String> m = Json.parseObject(
            "{\"id\":\"7\",\"conn\":\"1\",\"op\":\"QUERY\",\"sql\":\"SELECT a, b FROM t WHERE c='x, y'\",\"family\":\"oracle\"}");
        eq("comma-in-string", m.get("sql"), "SELECT a, b FROM t WHERE c='x, y'");
        eq("field-after-comma", m.get("family"), "oracle");
        // 转义
        Map<String, String> e = Json.parseObject("{\"error\":\"line 1\\nbroken \\\"quote\\\"\"}");
        eq("escaped-quote", e.get("error"), "line 1\nbroken \"quote\"");
        // 数字字段
        Map<String, String> n = Json.parseObject("{\"id\":42,\"ok\":true}");
        eq("number", n.get("id"), "42");
        eq("bool", n.get("ok"), "true");
        if (fails > 0) { System.exit(1); }
        System.out.println("JsonTest OK");
    }
}
