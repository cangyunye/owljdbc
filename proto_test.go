package owljdbc

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

func TestValueRoundTrip(t *testing.T) {
	vals := []any{
		nil, true, int64(-42), 3.14, "hello", []byte{1, 2, 3}, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	for _, v := range vals {
		b := encodeValue(nil, v)
		got, n, err := decodeValue(b)
		if err != nil {
			t.Fatalf("decode(%v): %v", v, err)
		}
		if n != len(b) {
			t.Fatalf("decode consumed %d, want %d", n, len(b))
		}
		if v == nil && got != nil {
			t.Fatalf("nil mismatch")
		}
		if !reflect.DeepEqual(got, v) {
			t.Fatalf("roundtrip %v -> %v", v, got)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, FrameRequest, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	ft, p, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameRequest || string(p) != "abc" {
		t.Fatalf("got %v %q", ft, p)
	}
}

func TestRequestPayload(t *testing.T) {
	req := ControlRequest{ID: 7, Conn: 1, Op: "EXEC", SQL: "INSERT INTO t VALUES(?,?)", Family: "mysql"}
	payload := encodeRequest(req, []any{int64(1), "x"})
	r2, args, err := decodeRequestPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if r2.ID != 7 || r2.Op != "EXEC" || r2.Family != "mysql" {
		t.Fatalf("header mismatch %+v", r2)
	}
	if len(args) != 2 || args[0].(int64) != 1 || args[1].(string) != "x" {
		t.Fatalf("args mismatch %+v", args)
	}
}

// TestDatetimeZoneRoundTrip verifies a non-UTC zone offset round-trips:
// encodeValue must store the offset as signed int16 minutes and decodeValue
// must reconstruct the same offset in seconds.
func TestDatetimeZoneRoundTrip(t *testing.T) {
	v := time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("X", 19800)) // UTC+5:30
	b := encodeValue(nil, v)
	got, n, err := decodeValue(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(b) {
		t.Fatalf("decode consumed %d, want %d", n, len(b))
	}
	gt, ok := got.(time.Time)
	if !ok {
		t.Fatalf("got %T, want time.Time", got)
	}
	if gt.UnixMilli() != v.UnixMilli() {
		t.Fatalf("epoch millis mismatch: got %d want %d", gt.UnixMilli(), v.UnixMilli())
	}
	_, off := gt.Zone()
	if off != 19800 {
		t.Fatalf("zone offset = %d, want 19800", off)
	}
}

// TestReadFrameZeroLength verifies readFrame rejects a zero-length frame
// instead of panicking on buf[0].
func TestReadFrameZeroLength(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 0})
	_, _, err := readFrame(&buf)
	if err == nil {
		t.Fatal("expected error for zero-length frame, got nil")
	}
}
