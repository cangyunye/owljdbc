package owl.agent;

import java.io.EOFException;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.nio.charset.StandardCharsets;
import java.sql.Timestamp;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

public final class Protocol {
    public static final int REQUEST = 0x01, RESPONSE = 0x02, ROW_BATCH = 0x03, END = 0x04;
    public static final int MAX_FRAME = 16 * 1024 * 1024;

    /** 读一帧，返回 [type][payload]；流结束抛 EOFException。 */
    public static byte[] readFrame(InputStream in) throws IOException {
        byte[] hdr = new byte[4];
        readFully(in, hdr);
        int total = intLE(hdr, 0);
        if (total < 1 || total > MAX_FRAME) throw new IOException("bad frame len " + total);
        byte[] buf = new byte[total];
        readFully(in, buf);
        return buf;
    }

    public static void writeFrame(OutputStream out, int type, byte[] payload) throws IOException {
        int total = 1 + payload.length;
        byte[] head = new byte[4 + 1 + payload.length];
        putIntLE(head, 0, total);
        head[4] = (byte) type;
        System.arraycopy(payload, 0, head, 5, payload.length);
        out.write(head);
        out.flush();
    }

    public static final class Request {
        public int id, conn;
        public String op = "", sql = "", family = "";
        public Object[] args = new Object[0];
    }

    /** payload = [u32 headerLen][header JSON][binary args...]（不含 type 字节）。 */
    public static Request parseRequest(byte[] payload) {
        ByteBuffer bb = ByteBuffer.wrap(payload).order(ByteOrder.LITTLE_ENDIAN);
        int hLen = bb.getInt();
        byte[] hb = new byte[hLen];
        bb.get(hb);
        Map<String, String> h = Json.parseObject(new String(hb, StandardCharsets.UTF_8));
        Request r = new Request();
        r.id = (int) longOf(h.get("id"));
        r.conn = (int) longOf(h.get("conn"));
        r.op = h.getOrDefault("op", "");
        r.sql = h.getOrDefault("sql", "");
        r.family = h.getOrDefault("family", "");
        List<Object> args = new ArrayList<>();
        while (bb.remaining() > 0) {
            args.add(decodeValue(bb));
        }
        r.args = args.toArray();
        return r;
    }

    private static long longOf(String s) {
        try { return s == null ? 0 : Long.parseLong(s.trim()); } catch (NumberFormatException e) { return 0; }
    }

    public static Object decodeValue(ByteBuffer bb) {
        int tag = bb.get() & 0xFF;
        switch (tag) {
            case ValueCodec.NULL: return null;
            case ValueCodec.BOOL: return bb.get() != 0;
            case ValueCodec.INT64: return bb.getLong();
            case ValueCodec.FLOAT64: return bb.getDouble();
            case ValueCodec.DECIMAL:
            case ValueCodec.STRING:
            case ValueCodec.DATE:
            case ValueCodec.TIME:
                return str(bb);
            case ValueCodec.BYTES: {
                byte[] b = new byte[bb.getInt()];
                bb.get(b);
                return b;
            }
            case ValueCodec.DATETIME: {
                long millis = bb.getLong();
                short tzMin = bb.getShort();
                Timestamp ts = new Timestamp(millis);
                ts.setNanos(0);
                // 时区语义保留给 Go 侧（Go decodeValue 自行还原 offset），此处仅承载 millis。
                return ts;
            }
            default: return null;
        }
    }

    private static String str(ByteBuffer bb) {
        int n = bb.getInt();
        byte[] b = new byte[n];
        bb.get(b);
        return new String(b, StandardCharsets.UTF_8);
    }

    static void putIntLE(byte[] b, int off, int v) {
        b[off] = (byte) v; b[off + 1] = (byte) (v >>> 8);
        b[off + 2] = (byte) (v >>> 16); b[off + 3] = (byte) (v >>> 24);
    }
    static int intLE(byte[] b, int off) {
        return (b[off] & 0xFF) | ((b[off + 1] & 0xFF) << 8)
            | ((b[off + 2] & 0xFF) << 16) | ((b[off + 3] & 0xFF) << 24);
    }
    private static void readFully(InputStream in, byte[] buf) throws IOException {
        int off = 0;
        while (off < buf.length) {
            int n = in.read(buf, off, buf.length - off);
            if (n < 0) throw new EOFException();
            off += n;
        }
    }
}
