package owl.agent.test;

import owl.agent.ValueCodec;
import java.math.BigDecimal;
import java.sql.Timestamp;
import java.util.Calendar;
import java.util.TimeZone;

public class ValueCodecTest {
    static int fails = 0;
    static void check(boolean ok, String name) {
        if (!ok) { System.out.println("FAIL " + name); fails++; }
    }
    public static void main(String[] a) {
        check(ValueCodec.encodeValue(null).length == 1, "null tag");
        // STRING 精确字节布局：tag(05) + len LE(02 00 00 00) + payload("OK"=4f 4b)。
        // 防止 concat destPos 回归把布局破坏成 [tag][payload][zeros]。
        byte[] s = ValueCodec.encodeValue("OK");
        byte[] want = {(byte) 0x05, 0x02, 0x00, 0x00, 0x00, 0x4f, 0x4b};
        check(java.util.Arrays.equals(s, want), "string exact layout [05][lenLE][payload]");
        byte[] d = ValueCodec.encodeValue(new BigDecimal("123.45"));
        check(d[0] == ValueCodec.DECIMAL, "decimal tag");
        byte[] b = ValueCodec.encodeValue(new byte[]{1, 2, 3});
        check(b[0] == ValueCodec.BYTES, "bytes tag");
        // DATETIME：tag + 8B millis + 2B tz minutes int16 LE = 11 字节
        Calendar cal = Calendar.getInstance(TimeZone.getTimeZone("GMT+08:00"));
        Timestamp ts = new Timestamp(1700000000000L);
        ts.setNanos(0);
        byte[] t = ValueCodec.encodeDatetime(ts, 480);
        check(t.length == 11 && t[0] == ValueCodec.DATETIME, "datetime len/tag");
        check((t[9] & 0xFF) == 0xE0 && t[10] == 0x01, "tz 480 min int16 LE (0x01E0)");
        if (fails > 0) { System.exit(1); }
        System.out.println("ValueCodecTest OK");
    }
}
