package owl.agent;

import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.sql.Types;

public final class Session {
    private final Connection conn;
    private volatile Statement current;

    Session(String driverClass, String url, String user, String pass) throws Exception {
        Class.forName(driverClass);
        this.conn = DriverManager.getConnection(url, user, pass);
    }

    /** JDBC 原生探活（q2 决议）。 */
    boolean isValid() throws SQLException {
        return conn.isValid(5);
    }

    void cancelCurrent() {
        Statement st = current;
        if (st != null) {
            try { st.cancel(); } catch (SQLException ignored) { }
        }
    }

    void clearCurrent() { current = null; }

    /** 返回受影响行数（DML）；DDL 返回 0。 */
    long exec(String sql, String family, Object[] args) throws SQLException {
        PreparedStatement ps = conn.prepareStatement(BindRewriter.rewrite(sql));
        current = ps;
        try {
            bind(ps, args);
            if (ps.execute()) {
                closeQuietly(ps.getResultSet());
                return 0;
            }
            return ps.getUpdateCount();
        } finally {
            current = null;
            ps.close();
        }
    }

    private static void closeQuietly(ResultSet rs) {
        if (rs != null) {
            try { rs.close(); } catch (SQLException ignored) { }
        }
    }

    ResultSet query(String sql, String family, Object[] args) throws SQLException {
        PreparedStatement ps = conn.prepareStatement(BindRewriter.rewrite(sql));
        current = ps;
        bind(ps, args);
        return ps.executeQuery();   // Main finally: rs.close(); ps.close(); current = null;
    }

    private void bind(PreparedStatement ps, Object[] args) throws SQLException {
        if (args == null) return;
        for (int i = 0; i < args.length; i++) {
            Object v = args[i];
            if (v == null) ps.setNull(i + 1, Types.NULL);
            else if (v instanceof Long) ps.setLong(i + 1, (Long) v);
            else if (v instanceof Integer) ps.setInt(i + 1, (Integer) v);
            else if (v instanceof Double) ps.setDouble(i + 1, (Double) v);
            else if (v instanceof Boolean) ps.setBoolean(i + 1, (Boolean) v);
            else if (v instanceof byte[]) ps.setBytes(i + 1, (byte[]) v);
            else ps.setString(i + 1, String.valueOf(v));
        }
    }

    void begin() throws SQLException { conn.setAutoCommit(false); }
    void commit() throws SQLException { conn.commit(); conn.setAutoCommit(true); }
    void rollback() throws SQLException { conn.rollback(); conn.setAutoCommit(true); }
    void close() throws SQLException { conn.close(); }
}
