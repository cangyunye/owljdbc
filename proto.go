package owljdbc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

type FrameType uint8

const (
	FrameRequest  FrameType = 0x01
	FrameResponse FrameType = 0x02
	FrameRowBatch FrameType = 0x03
	FrameEnd      FrameType = 0x04
)

type ColMeta struct {
	Name string `json:"name"`
	Type string `json:"type"` // 原始数据库类型（大写）
}

type ControlRequest struct {
	ID     uint32 `json:"id"`
	Conn   uint32 `json:"conn"`
	Op     string `json:"op"`
	SQL    string `json:"sql,omitempty"`
	Family string `json:"family,omitempty"`
}

type ControlResponse struct {
	ID       uint32    `json:"id"`
	Conn     uint32    `json:"conn"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Rows     int64     `json:"rows,omitempty"`
	Affected int64     `json:"affected,omitempty"`
	Cols     []ColMeta `json:"cols,omitempty"`
}

const (
	tagNull     = 0x00
	tagBool     = 0x01
	tagInt64    = 0x02
	tagFloat64  = 0x03
	tagDecimal  = 0x04
	tagString   = 0x05
	tagBytes    = 0x06
	tagDatetime = 0x07
	tagDate     = 0x08
	tagTime     = 0x09
)

func writeFrame(w io.Writer, t FrameType, payload []byte) error {
	total := 1 + len(payload)
	hdr := make([]byte, 4)
	binary.LittleEndian.PutUint32(hdr, uint32(total))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if _, err := w.Write([]byte{byte(t)}); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

const maxFrameSize = 16 << 20 // 16 MiB

func readFrame(r io.Reader) (FrameType, []byte, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	total := binary.LittleEndian.Uint32(hdr)
	if total < 1 {
		return 0, nil, errors.New("frame too small")
	}
	if total > maxFrameSize {
		return 0, nil, errors.New("frame too large")
	}
	buf := make([]byte, total)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return FrameType(buf[0]), buf[1:], nil
}

// encodeValue appends the encoded form of v to buf. Runtime-type based (for args).
func encodeValue(buf []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(buf, tagNull)
	case bool:
		buf = append(buf, tagBool)
		if x {
			return append(buf, 1)
		}
		return append(buf, 0)
	case int:
		return encodeValue(buf, int64(x))
	case int32:
		return encodeValue(buf, int64(x))
	case int64:
		buf = append(buf, tagInt64)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(x))
		return append(buf, b[:]...)
	case float64:
		buf = append(buf, tagFloat64)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(x))
		return append(buf, b[:]...)
	case string:
		buf = append(buf, tagString)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(x)))
		return append(buf, x...)
	case []byte:
		buf = append(buf, tagBytes)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(x)))
		return append(buf, x...)
	case time.Time:
		buf = append(buf, tagDatetime)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(x.UnixMilli()))
		buf = append(buf, b[:]...)
		_, off := x.Zone()
		minutes := int16(off / 60)
		var zb [2]byte
		binary.LittleEndian.PutUint16(zb[:], uint16(minutes))
		return append(buf, zb[:]...)
	default:
		panic(fmt.Sprintf("encodeValue: unsupported %T", v))
	}
}

func decodeValue(b []byte) (any, int, error) {
	if len(b) < 1 {
		return nil, 0, errors.New("empty value")
	}
	tag := b[0]
	switch tag {
	case tagNull:
		return nil, 1, nil
	case tagBool:
		if len(b) < 2 {
			return nil, 0, errors.New("short bool")
		}
		return b[1] != 0, 2, nil
	case tagInt64:
		if len(b) < 9 {
			return nil, 0, errors.New("short int")
		}
		return int64(binary.LittleEndian.Uint64(b[1:])), 9, nil
	case tagFloat64:
		if len(b) < 9 {
			return nil, 0, errors.New("short float")
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(b[1:])), 9, nil
	case tagDecimal, tagString, tagBytes, tagDate, tagTime:
		if len(b) < 5 {
			return nil, 0, errors.New("short len")
		}
		n := int(binary.LittleEndian.Uint32(b[1:]))
		if len(b) < 5+n {
			return nil, 0, errors.New("short payload")
		}
		payload := b[5 : 5+n]
		if tag == tagBytes {
			return append([]byte(nil), payload...), 5 + n, nil
		}
		return string(payload), 5 + n, nil
	case tagDatetime:
		if len(b) < 11 {
			return nil, 0, errors.New("short datetime")
		}
		ms := int64(binary.LittleEndian.Uint64(b[1:]))
		minutes := int16(binary.LittleEndian.Uint16(b[9:11]))
		t := time.UnixMilli(ms)
		if minutes == 0 {
			return t.UTC(), 11, nil
		}
		loc := time.FixedZone("", int(minutes)*60)
		return t.In(loc), 11, nil
	default:
		return nil, 0, fmt.Errorf("unknown tag %d", tag)
	}
}

// encodeRequest builds a REQUEST payload: [u32 headerLen][header JSON][binary args...]
func encodeRequest(req ControlRequest, args []any) []byte {
	header, _ := json.Marshal(req)
	out := binary.LittleEndian.AppendUint32(nil, uint32(len(header)))
	out = append(out, header...)
	for _, a := range args {
		out = encodeValue(out, a)
	}
	return out
}

func decodeRequestPayload(p []byte) (ControlRequest, []any, error) {
	if len(p) < 4 {
		return ControlRequest{}, nil, errors.New("short request")
	}
	hLen := int(binary.LittleEndian.Uint32(p[:4]))
	if len(p) < 4+hLen {
		return ControlRequest{}, nil, errors.New("short header")
	}
	var req ControlRequest
	if err := json.Unmarshal(p[4:4+hLen], &req); err != nil {
		return ControlRequest{}, nil, err
	}
	var args []any
	rest := p[4+hLen:]
	for len(rest) > 0 {
		v, n, err := decodeValue(rest)
		if err != nil {
			return ControlRequest{}, nil, err
		}
		args = append(args, v)
		rest = rest[n:]
	}
	return req, args, nil
}

func ReadFrame(r io.Reader) (FrameType, []byte, error)             { return readFrame(r) }
func WriteFrame(w io.Writer, t FrameType, payload []byte) error    { return writeFrame(w, t, payload) }
func DecodeRequestPayload(p []byte) (ControlRequest, []any, error) { return decodeRequestPayload(p) }

// EncodeRowBatch 编码一行到 ROW_BATCH payload：[u32 conn][u32 id][u32 rowCount][values...]
func EncodeRowBatch(conn, id uint32, row []any) []byte {
	out := binary.LittleEndian.AppendUint32(nil, conn)
	out = binary.LittleEndian.AppendUint32(out, id)
	out = binary.LittleEndian.AppendUint32(out, 1)
	for _, v := range row {
		out = encodeValue(out, v)
	}
	return out
}

// DecodeRowBatch 是 decodeRowBatch 的导出对称面（EncodeRowBatch 的逆操作），
// 供测试 harness 与工具进程解码 ROW_BATCH payload。
func DecodeRowBatch(p []byte) (conn, id uint32, rows [][]any, err error) {
	return decodeRowBatch(p)
}

// decodeRowBatch 解码 ROW_BATCH payload。
// 值解码失败时仍返回已解析的 conn/id 头：头独立于值区域，即使值损坏也必须
// 能把错误路由回对应会话的在读流（readLoop 依赖这一点做 fail-fast）。
func decodeRowBatch(p []byte) (conn, id uint32, rows [][]any, err error) {
	if len(p) < 12 {
		return 0, 0, nil, errors.New("short rowbatch")
	}
	conn = binary.LittleEndian.Uint32(p[0:4])
	id = binary.LittleEndian.Uint32(p[4:8])
	count := binary.LittleEndian.Uint32(p[8:12])
	rest := p[12:]
	rows = make([][]any, 0, count)
	for i := uint32(0); i < count; i++ {
		var row []any
		for len(rest) > 0 {
			v, n, verr := decodeValue(rest)
			if verr != nil {
				return conn, id, nil, verr
			}
			row = append(row, v)
			rest = rest[n:]
		}
		rows = append(rows, row)
	}
	return conn, id, rows, nil
}
