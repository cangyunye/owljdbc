package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	agent "github.com/cangyunye/owljdbc"
)

func main() {
	for {
		ft, payload, err := agent.ReadFrame(os.Stdin)
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintln(os.Stderr, err)
			return
		}
		if ft != agent.FrameRequest {
			continue
		}
		req, _, err := agent.DecodeRequestPayload(payload)
		if err != nil {
			continue
		}
		switch req.Op {
		case "CONNECT", "PING", "BEGIN", "COMMIT", "ROLLBACK", "CLOSE":
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: true})
		case "QUERY":
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: true, Cols: []agent.ColMeta{{Name: "X", Type: "VARCHAR"}}})
			if strings.Contains(req.SQL, "__MIDSTREAM_FAIL__") {
				// 中途失败剧本：一列、一行，然后 END(ok:false) 而非干净收尾
				writeRow(os.Stdout, req, []any{"hello"})
				writeEndErr(os.Stdout, req, "boom")
			} else if row, ok := mockProbeRow(req.SQL); ok {
				// 字符集探针剧本：假定 OB-Oracle UTF8 租户 / MySQL utf8mb4 / PG UTF8 服务端
				writeRow(os.Stdout, req, []any{row})
				writeEnd(os.Stdout, req, 1)
			} else {
				writeRow(os.Stdout, req, []any{"hello"})
				writeEnd(os.Stdout, req, 1)
			}
		case "EXEC":
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: true, Affected: 3})
		default:
			writeResp(os.Stdout, req, agent.ControlResponse{ID: req.ID, OK: false, Error: "unknown op"})
		}
	}
}

// mockProbeRow 把字符集探针 SQL 映射为固定的服务端返回值，模拟一个
// OB-Oracle UTF8 租户 / MySQL utf8mb4 / PostgreSQL UTF8 服务端，供
// dbconn.ProbeServerEncoding 在无真实数据库时验证探针与分类逻辑。
func mockProbeRow(sql string) (string, bool) {
	switch {
	case strings.Contains(sql, "NLS_CHARACTERSET"):
		return "AL32UTF8", true
	case strings.Contains(sql, "character_set_server"):
		return "utf8mb4", true
	case strings.Contains(sql, "server_encoding"):
		return "UTF8", true
	default:
		return "", false
	}
}

func writeResp(w io.Writer, req agent.ControlRequest, resp agent.ControlResponse) {
	// 回填 conn-id：客户端 readLoop 按 Conn 路由响应到对应 session。
	resp.Conn = req.Conn
	b, _ := json.Marshal(resp)
	agent.WriteFrame(w, agent.FrameResponse, b)
}

func writeRow(w io.Writer, req agent.ControlRequest, row []any) {
	agent.WriteFrame(w, agent.FrameRowBatch, agent.EncodeRowBatch(req.Conn, req.ID, row))
}

func writeEnd(w io.Writer, req agent.ControlRequest, rows int64) {
	b, _ := json.Marshal(agent.ControlResponse{ID: req.ID, Conn: req.Conn, OK: true, Rows: rows})
	agent.WriteFrame(w, agent.FrameEnd, b)
}

func writeEndErr(w io.Writer, req agent.ControlRequest, msg string) {
	b, _ := json.Marshal(agent.ControlResponse{ID: req.ID, Conn: req.Conn, OK: false, Error: msg})
	agent.WriteFrame(w, agent.FrameEnd, b)
}
