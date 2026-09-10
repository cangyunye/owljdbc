package owl.agent;

import java.io.ByteArrayOutputStream;
import java.math.BigDecimal;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.nio.charset.StandardCharsets;
import java.sql.Date;
import java.sql.Time;
import java.sql.Timestamp;

/** Go/Java 共享的值编码（tag 布局见计划 Global Constraints）。小端。 */
public final class ValueCodec {
    public static final int NULL = 0x00, BOOL = 0x01, INT64 = 0x02, FLOAT64 = 0x03,
        DECIMAL = 0x04, STRING = 0x05, BYTES = 0x06, DATETIME = 0x07, DATE = 0x08, TIME = 0x09;

    public static byte[] encodeValue(Object v) {
        if (v == null) return tag(NULL);
        if (v instanceof Boolean) return bytes(tag(BOOL), new byte[]{(byte)(((Boolean) v) ? 1 : 0)});
        if (v instanceof Integer) return longBytes(INT64, ((Integer) v).longValue());
        if (v instanceof Long) return longBytes(INT64, (Long) v);
        if (v instanceof Float) return doubleBytes(FLOAT64, ((Float) v).doubleValue());
        if (v instanceof Double) return doubleBytes(FLOAT64, (Double) v);
        if (v instanceof BigDecimal) return textBytes(DECIMAL, ((BigDecimal) v).toPlainString());
        if (v instanceof Timestamp) return encodeDatetime((Timestamp) v);
        if (v instanceof Date) {                       // DATE → DATETIME（午夜），Go 侧还原 time.Time 保证导出对拍一致
            Date d = (Date) v;
            Timestamp ts = new Timestamp(d.getTime());
            return encodeDatetime(ts);
        }
        if (v instanceof Time) return textBytes(TIME, ((Time) v).toString());
        if (v instanceof byte[]) {
            byte[] b = (byte[]) v;
            return bytes(tag(BYTES), concat(intBytes(b.length), b));
        }
        return textBytes(STRING, String.valueOf(v));
    }

    /** DATETIME：tag + 8B epoch millis LE + 2B tz minutes int16 LE（与 Go decodeValue 对齐）。 */
    public static byte[] encodeDatetime(Timestamp ts) {
        int offsetMinutes;
        java.util.TimeZone tz = java.util.TimeZone.getDefault();
        offsetMinutes = tz.getOffset(ts.getTime()) / 60000;
        return encodeDatetime(ts, offsetMinutes);
    }

    public static byte[] encodeDatetime(Timestamp ts, int offsetMinutes) {
        ByteBuffer bb = ByteBuffer.allocate(11).order(ByteOrder.LITTLE_ENDIAN);
        bb.put((byte) DATETIME);
        bb.putLong(ts.getTime());
        bb.putShort((short) offsetMinutes);
        return bb.array();
    }

    private static byte[] tag(int t) { return new byte[]{(byte) t}; }
    private static byte[] longBytes(int tag, long v) {
        return bytes(tag(tag), ByteBuffer.allocate(8).order(ByteOrder.LITTLE_ENDIAN).putLong(v).array());
    }
    private static byte[] doubleBytes(int tag, double v) {
        return bytes(tag(tag), ByteBuffer.allocate(8).order(ByteOrder.LITTLE_ENDIAN).putDouble(v).array());
    }
    private static byte[] textBytes(int tag, String s) {
        byte[] b = s.getBytes(StandardCharsets.UTF_8);
        return bytes(tag(tag), concat(intBytes(b.length), b));
    }

    private static byte[] concat(byte[] a, byte[] b) {
        byte[] out = new byte[a.length + b.length];
        System.arraycopy(a, 0, out, 0, a.length);
        System.arraycopy(b, 0, out, a.length, b.length);
        return out;
    }
    private static byte[] intBytes(int n) { return ByteBuffer.allocate(4).order(ByteOrder.LITTLE_ENDIAN).putInt(n).array(); }
    private static byte[] bytes(byte[]... parts) {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        for (byte[] p : parts) o.write(p, 0, p.length);
        return o.toByteArray();
    }
}
