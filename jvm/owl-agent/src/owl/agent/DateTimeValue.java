package owl.agent;

import java.sql.Timestamp;

/** DATETIME 绑定值：毫秒时间戳 + 编码端时区偏移（分钟）。墙钟语义在
 * setTimestamp(idx, ts, cal) 时用该偏移还原，保证 naive 时间戳跨库不漂移。 */
public final class DateTimeValue {
    public final Timestamp ts;
    public final int offsetMinutes;

    public DateTimeValue(Timestamp ts, int offsetMinutes) {
        this.ts = ts;
        this.offsetMinutes = offsetMinutes;
    }

    public java.util.TimeZone tz() {
        return new java.util.SimpleTimeZone(offsetMinutes * 60_000, "owl-offset");
    }
}
