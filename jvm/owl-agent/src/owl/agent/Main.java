package owl.agent;

import java.io.EOFException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.nio.charset.StandardCharsets;
import java.sql.ResultSet;
import java.sql.ResultSetMetaData;
import java.sql.Statement;
import java.sql.Types;
import java.util.Arrays;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class Main {
    static final Map<Integer, Session> SESSIONS = new ConcurrentHashMap<>();

    public static void main(String[] args) throws Exception {
        InputStream in = System.in;
        OutputStream out = System.out;
        ExecutorService pool = Executors.newCachedThreadPool();
        while (true) {
            byte[] frame;
            try {
                frame = Protocol.readFrame(in);
            } catch (EOFException e) {
                break;
            }
            if (frame.length < 1) continue;
            if ((frame[0] & 0xFF) != Protocol.REQUEST) continue;
            final byte[] payload = Arrays.copyOfRange(frame, 1, frame.length);
            pool.submit(() -> handle(payload, out));
        }
        pool.shutdown();
    }

    static void handle(byte[] payload, OutputStream out) {
        Protocol.Request req = Protocol.parseRequest(payload);
        if (System.getenv("OWL_AGENT_DEBUG") != null) {
            System.err.println("[agent] op=" + req.op + " sqlNull=" + (req.sql == null)
                + " sqlLen=" + (req.sql == null ? -1 : req.sql.length()));
        }
        try {
            switch (req.op) {
                case "CONNECT": {
                    Session s = new Session(str(req.args, 0), str(req.args, 1), str(req.args, 2), str(req.args, 3));
                    SESSIONS.put(req.conn, s);
                    sendResp(out, req, true, null, null, 0, 0);
                    break;
                }
                case "CLOSE": {
                    Session s = SESSIONS.remove(req.conn);
                    if (s != null) s.close();
                    sendResp(out, req, true, null, null, 0, 0);
                    break;
                }
                case "PING": {
                    Session s = SESSIONS.get(req.conn);
                    boolean ok = s == null || s.isValid();   // probe 会话允许无连接
                    sendResp(out, req, ok, null, ok ? null : "not valid", 0, 0);
                    break;
                }
                case "BEGIN": SESSIONS.get(req.conn).begin(); sendResp(out, req, true, null, null, 0, 0); break;
                case "COMMIT": SESSIONS.get(req.conn).commit(); sendResp(out, req, true, null, null, 0, 0); break;
                case "ROLLBACK": SESSIONS.get(req.conn).rollback(); sendResp(out, req, true, null, null, 0, 0); break;
                case "CANCEL": {
                    Session s = SESSIONS.get(req.conn);
                    if (s != null) s.cancelCurrent();
                    break;   // best-effort，不回包（Go 不等待）
                }
                case "EXEC": {
                    long affected = SESSIONS.get(req.conn).exec(req.sql, req.family, req.args);
                    sendResp(out, req, true, null, null, 0, affected);
                    break;
                }
                case "QUERY": {
                    Session s = SESSIONS.get(req.conn);
                    ResultSet rs = null;
                    Statement ps = null;
                    boolean headerSent = false;
                    long rows = 0;
                    try {
                        try {
                            rs = s.query(req.sql, req.family, req.args);
                            ps = rs.getStatement();
                            ResultSetMetaData md = rs.getMetaData();
                            StringBuilder cols = new StringBuilder("[");
                            for (int i = 1; i <= md.getColumnCount(); i++) {
                                if (i > 1) cols.append(',');
                                // 常量列（如 information_schema 查询里的 '' / 'SQL'）的
                                // label/typeName 可能为 null（Connector/J 实测），必须兜底。
                                String label = md.getColumnLabel(i);
                                String typeName = md.getColumnTypeName(i);
                                cols.append("{\"name\":\"").append(Json.escape(label == null ? "" : label))
                                    .append("\",\"type\":\"").append(Json.escape(typeName == null ? "" : typeName)).append("\"}");
                            }
                            cols.append(']');
                            sendRespRaw(out, req, true, cols.toString(), null, 0, 0);
                            headerSent = true;
                            int nCols = md.getColumnCount();
                            while (rs.next()) {
                                // 先把整行编码进字节数组，再按 12 字节头 + 行负载精确分配缓冲
                                java.io.ByteArrayOutputStream row = new java.io.ByteArrayOutputStream();
                                for (int i = 1; i <= nCols; i++) {
                                    row.write(ValueCodec.encodeValue(columnValue(rs, md, i, req.family)));
                                }
                                byte[] rowBytes = row.toByteArray();
                                ByteBuffer bb = ByteBuffer.allocate(12 + rowBytes.length).order(ByteOrder.LITTLE_ENDIAN);
                                bb.putInt(req.conn);
                                bb.putInt(req.id);
                                bb.putInt(1);
                                bb.put(rowBytes);
                                Protocol.writeFrame(out, Protocol.ROW_BATCH, bb.array());
                                rows++;
                            }
                            sendEnd(out, req, rows, null);
                        } catch (Exception e) {
                            // 表头已发出后 Go 端 pending[id] 已删除：错误 RESPONSE 无人接收，
                            // 必须以 END(ok:false) 收尾，否则 Go 的 Next() 永久阻塞。
                            if (!headerSent) throw e;
                            sendEnd(out, req, rows, e.getMessage() != null ? e.getMessage() : e.toString());
                        }
                    } finally {
                        if (rs != null) rs.close();
                        if (ps != null) ps.close();
                        if (s != null) s.clearCurrent();
                    }
                    break;
                }
                case "SHUTDOWN": System.exit(0); break;
                default:
                    sendResp(out, req, false, null, "unknown op " + req.op, 0, 0);
            }
        } catch (Exception e) {
            if (System.getenv("OWL_AGENT_DEBUG") != null) {
                e.printStackTrace();
            }
            try {
                sendResp(out, req, false, null, String.valueOf(e.getMessage()), 0, 0);
            } catch (Exception ignored) { }
        }
    }

    /** 类型感知取值：BLOB/二进制 → getBytes；CLOB/文本大对象/数值 → getString（保留驱动原始渲染）；其余 getObject。 */
    static Object columnValue(ResultSet rs, ResultSetMetaData md, int i, String family) throws Exception {
        int type = md.getColumnType(i);
        switch (type) {
            case Types.BINARY: case Types.VARBINARY: case Types.LONGVARBINARY:
            case Types.BLOB:
                return rs.getBytes(i);
            case Types.CLOB: case Types.NCLOB: case Types.LONGVARCHAR: case Types.LONGNVARCHAR:
            case Types.NUMERIC: case Types.DECIMAL: case Types.FLOAT: case Types.DOUBLE: case Types.REAL:
            case Types.DATE: case Types.TIME: case Types.TIMESTAMP:
                // 日期时间按 family 分治：仅 mysql 族用驱动原样渲染（native
                // go-sql-driver 无 parseTime 时返回原样字符串）；postgres 族
                // （lib/pq 系 native 返回 time.Time）与 oracle 族保持
                // getObject→LocalDateTime→datetime tag→Go time.Time，两侧
                // CSV 均落 exporter 的紧凑 datetime 格式。
                if ("mysql".equals(family)) {
                    return rs.getString(i);
                }
                return rs.getObject(i);
            default:
                return rs.getObject(i);
        }
    }

    static String str(Object[] args, int i) {
        return args != null && i < args.length && args[i] != null ? String.valueOf(args[i]) : "";
    }

    static void sendResp(OutputStream out, Protocol.Request req, boolean ok,
                         StringBuilder cols, String err, long rows, long affected) throws Exception {
        sendRespRaw(out, req, ok, cols == null ? null : cols.toString(), err, rows, affected);
    }

    static void sendRespRaw(OutputStream out, Protocol.Request req, boolean ok,
                            String colsJson, String err, long rows, long affected) throws Exception {
        StringBuilder b = new StringBuilder();
        b.append("{\"id\":").append(req.id).append(",\"conn\":").append(req.conn)
            .append(",\"ok\":").append(ok);
        if (err != null) b.append(",\"error\":\"").append(Json.escape(err)).append('"');
        if (colsJson != null) b.append(",\"cols\":").append(colsJson);
        if (rows > 0) b.append(",\"rows\":").append(rows);
        if (affected > 0) b.append(",\"affected\":").append(affected);
        b.append('}');
        Protocol.writeFrame(out, Protocol.RESPONSE, b.toString().getBytes(StandardCharsets.UTF_8));
    }

    static void sendEnd(OutputStream out, Protocol.Request req, long rows, String err) throws Exception {
        StringBuilder b = new StringBuilder();
        b.append("{\"id\":").append(req.id).append(",\"conn\":").append(req.conn)
            .append(",\"ok\":").append(err == null).append(",\"rows\":").append(rows);
        if (err != null) b.append(",\"error\":\"").append(Json.escape(err)).append('"');
        b.append('}');
        Protocol.writeFrame(out, Protocol.END, b.toString().getBytes(StandardCharsets.UTF_8));
    }
}
