package owl.agent.test;

import owl.agent.BindRewriter;

public class BindRewriterTest {
    static int fails = 0;
    static void eq(String name, String in, String want) {
        String got = BindRewriter.rewrite(in);
        if (!want.equals(got)) {
            System.out.println("FAIL " + name + ": [" + in + "] -> [" + got + "] want [" + want + "]");
            fails++;
        }
    }
    public static void main(String[] a) {
        eq("basic", "SELECT * FROM t WHERE a=:1", "SELECT * FROM t WHERE a=?");
        eq("multi", "INSERT INTO t VALUES(:1,:2)", "INSERT INTO t VALUES(?,?)");
        eq("multi-digit", "INSERT INTO t VALUES(:10,:11)", "INSERT INTO t VALUES(?,?)");
        eq("literal-colon", "SELECT ':1' FROM t", "SELECT ':1' FROM t");
        eq("literal-comma-colon", "SELECT 'a,:1b' FROM t WHERE c=:2", "SELECT 'a,:1b' FROM t WHERE c=?");
        eq("line-comment", "SELECT 1 -- :1\n", "SELECT 1 -- :1\n");
        eq("block-comment", "SELECT /* :1 */ 2", "SELECT /* :1 */ 2");
        eq("double-colon-cast", "SELECT a::int FROM t WHERE b=:1", "SELECT a::int FROM t WHERE b=?");
        eq("assign", "BEGIN x:=1; END", "BEGIN x:=1; END");
        eq("qmark-passthru", "SELECT * FROM t WHERE a=?", "SELECT * FROM t WHERE a=?");
        eq("dquote-ident", "SELECT \":1\" FROM t WHERE b=:2", "SELECT \":1\" FROM t WHERE b=?");
        eq("no-bind", "SELECT 1", "SELECT 1");
        if (fails > 0) { System.exit(1); }
        System.out.println("BindRewriterTest OK");
    }
}
