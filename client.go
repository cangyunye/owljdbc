package owljdbc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// AgentProc 是一个 JVM sidecar 进程及其帧读写循环。
type AgentProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu       sync.Mutex
	writeMu  sync.Mutex // 序列化对 stdin 的整帧写入，防止并发 writeFrame 交错损坏帧流
	sessions map[uint32]*Session
	nextConn uint32
	closed   chan struct{} // 进程死亡信号：readLoop 读 stdout 到 EOF/error 时 close
	once     sync.Once
}

// Session 是一个逻辑连接（一个 conn-id），串行执行其语句。
type Session struct {
	proc       *AgentProc
	connID     uint32
	family     string
	mu         sync.Mutex // 串行化本 session 的请求（exec/Query 在等待期间持有）
	dispatchMu sync.Mutex // 保护 pending 与 curRows（readLoop 派发与 exec/Query 并发访问）
	nextID     uint32
	pending    map[uint32]chan ControlResponse
	curRows    *QueryStream
}

// QueryStream 流式读取查询结果。
type QueryStream struct {
	sess *Session
	id   uint32
	cols []ColMeta
	rows chan []any
	done chan struct{}
	once sync.Once
	err  error // 中途失败（END ok:false / ROW_BATCH 解码错误 / 进程中止）带给 Next()；在 close(qs.rows) 前写入
}

const startupTimeout = 30 * time.Second

func openAgent(ctx context.Context, cfg Config) (*AgentProc, error) {
	java := cfg.JavaHome
	if java == "" {
		java = "java"
	}
	cp := cfg.AgentJar
	for _, j := range cfg.Classpath {
		cp += ":" + j
	}
	cmd := exec.Command(java, "-cp", cp, "owl.agent.Main")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &AgentProc{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		stderr:   stderr,
		sessions: make(map[uint32]*Session),
		nextConn: 1,
		closed:   make(chan struct{}),
	}
	go p.readLoop()
	go p.logLoop(stderr)
	// 启动握手：PING 带超时，确认 JVM 就绪。
	if err := p.handshake(ctx); err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}

func (p *AgentProc) handshake(ctx context.Context) error {
	hctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	probe := &Session{proc: p, connID: 0, pending: make(map[uint32]chan ControlResponse)}
	// 注册 probe（conn-id 0）以便 readLoop 能按 Conn 路由回 PING 响应。
	p.mu.Lock()
	p.sessions[0] = probe
	p.mu.Unlock()
	_, err := probe.exec(hctx, ControlRequest{Op: "PING"}, nil)
	p.mu.Lock()
	delete(p.sessions, 0)
	p.mu.Unlock()
	return err
}

func (p *AgentProc) logLoop(stderr io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		if n > 0 {
			os.Stderr.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (p *AgentProc) markClosed() {
	p.once.Do(func() { close(p.closed) })
}

func (p *AgentProc) readLoop() {
	for {
		ft, payload, err := readFrame(p.stdout)
		if err != nil {
			p.markClosed()
			p.abortAll(errors.New("agent process closed"))
			return
		}
		switch ft {
		case FrameResponse:
			var resp ControlResponse
			if json.Unmarshal(payload, &resp) != nil {
				continue
			}
			p.mu.Lock()
			s := p.sessions[resp.Conn]
			p.mu.Unlock()
			if s != nil {
				s.dispatchResponse(resp)
			}
		case FrameRowBatch:
			// 值解码失败≠可以静默丢弃：12 字节头 (conn,id,count) 独立于值区域，
			// 仍然有效。把解码错误路由回该会话的在读流（fail-fast），而非变成
			// "no rows in result set"。
			conn, id, rows, err := decodeRowBatch(payload)
			p.mu.Lock()
			s := p.sessions[conn]
			p.mu.Unlock()
			if err != nil {
				if s != nil {
					s.abortStream(fmt.Errorf("row batch decode: %w", err))
				} else {
					// 头都无法解析（异常短帧，定位不到会话）：协议级损坏，中止所有会话。
					p.abortAll(fmt.Errorf("corrupt row batch: %w", err))
				}
				continue
			}
			if s != nil {
				s.dispatchRows(id, rows)
			}
		case FrameEnd:
			var resp ControlResponse
			if json.Unmarshal(payload, &resp) != nil {
				continue
			}
			p.mu.Lock()
			s := p.sessions[resp.Conn]
			p.mu.Unlock()
			if s != nil {
				s.dispatchEnd(resp)
			}
		}
	}
}

func (p *AgentProc) abortAll(err error) {
	p.mu.Lock()
	sessions := make([]*Session, 0, len(p.sessions))
	for _, s := range p.sessions {
		sessions = append(sessions, s)
	}
	p.mu.Unlock()
	for _, s := range sessions {
		s.abortStream(err)
	}
}

func (p *AgentProc) close() {
	p.markClosed()
	_ = p.cmd.Process.Kill()
}

// writeRequest 原子地写出一整个 REQUEST 帧。
// exec/Query/sendCancel 可能并发写同一 stdin，必须用 writeMu 保证帧不交错；
// writeMu 仅在帧写入期间持有，绝不在等待响应时持有（死锁规则）。
func (p *AgentProc) writeRequest(payload []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return writeFrame(p.stdin, FrameRequest, payload)
}

func (p *AgentProc) NewSession(cfg Config) (*Session, error) {
	p.mu.Lock()
	select {
	case <-p.closed:
		p.mu.Unlock()
		return nil, errors.New("agent process closed")
	default:
	}
	connID := p.nextConn
	p.nextConn++
	s := &Session{proc: p, connID: connID, family: cfg.Family, pending: make(map[uint32]chan ControlResponse)}
	p.sessions[connID] = s
	p.mu.Unlock()

	resp, err := s.exec(context.Background(), ControlRequest{Op: "CONNECT"},
		[]any{cfg.DriverClass, cfg.URL, cfg.User, cfg.Password})
	if err != nil {
		p.mu.Lock()
		delete(p.sessions, s.connID)
		p.mu.Unlock()
		return nil, err
	}
	if !resp.OK {
		p.mu.Lock()
		delete(p.sessions, s.connID)
		p.mu.Unlock()
		return nil, errors.New(resp.Error)
	}
	return s, nil
}

// exec 发送控制请求并等待响应；支持 ctx 取消（发 CANCEL）与进程死亡检测。
func (s *Session) exec(ctx context.Context, req ControlRequest, args []any) (ControlResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan ControlResponse, 1)
	id := s.nextID
	s.nextID++
	req.ID = id
	req.Conn = s.connID
	s.dispatchMu.Lock()
	s.pending[id] = ch
	s.dispatchMu.Unlock()
	defer func() {
		s.dispatchMu.Lock()
		delete(s.pending, id)
		s.dispatchMu.Unlock()
	}()

	if err := s.proc.writeRequest(encodeRequest(req, args)); err != nil {
		return ControlResponse{}, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		go s.sendCancel()
		return ControlResponse{}, ctx.Err()
	case <-s.proc.closed:
		return ControlResponse{}, errors.New("agent process closed")
	}
}

func (s *Session) sendCancel() {
	_ = s.proc.writeRequest(encodeRequest(ControlRequest{Conn: s.connID, Op: "CANCEL"}, nil))
}

func (s *Session) Exec(ctx context.Context, req ControlRequest, args []any) (ControlResponse, error) {
	return s.exec(ctx, req, args)
}

func (s *Session) Query(ctx context.Context, req ControlRequest, args []any) (*QueryStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan ControlResponse, 1)
	id := s.nextID
	s.nextID++
	req.ID = id
	req.Conn = s.connID
	s.dispatchMu.Lock()
	s.pending[id] = ch
	s.dispatchMu.Unlock()
	defer func() {
		s.dispatchMu.Lock()
		delete(s.pending, id)
		s.dispatchMu.Unlock()
	}()

	qs := &QueryStream{sess: s, id: id, rows: make(chan []any, 256), done: make(chan struct{})}
	s.dispatchMu.Lock()
	s.curRows = qs
	s.dispatchMu.Unlock()

	if err := s.proc.writeRequest(encodeRequest(req, args)); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if !resp.OK {
			// 放弃该流：撤销 curRows 并关闭 done，防止滞留的 ROW_BATCH
			// 在无读者的 qs.rows 上永久阻塞 dispatchRows/readLoop。
			s.dispatchMu.Lock()
			if s.curRows == qs {
				s.curRows = nil
			}
			s.dispatchMu.Unlock()
			qs.once.Do(func() { close(qs.done) })
			return nil, errors.New(resp.Error)
		}
		qs.cols = resp.Cols
		return qs, nil
	case <-ctx.Done():
		go s.sendCancel()
		s.dispatchMu.Lock()
		if s.curRows == qs {
			s.curRows = nil
		}
		s.dispatchMu.Unlock()
		qs.once.Do(func() { close(qs.done) })
		return nil, ctx.Err()
	case <-s.proc.closed:
		s.dispatchMu.Lock()
		if s.curRows == qs {
			s.curRows = nil
		}
		s.dispatchMu.Unlock()
		qs.once.Do(func() { close(qs.done) })
		return nil, errors.New("agent process closed")
	}
}

func (s *Session) dispatchResponse(resp ControlResponse) {
	s.dispatchMu.Lock()
	ch := s.pending[resp.ID]
	s.dispatchMu.Unlock()
	if ch != nil {
		ch <- resp
	}
}

func (s *Session) dispatchRows(id uint32, rows [][]any) {
	s.dispatchMu.Lock()
	qs := s.curRows
	s.dispatchMu.Unlock()
	if qs != nil && qs.id == id {
		for _, r := range rows {
			select {
			case qs.rows <- r:
			case <-qs.done:
				return
			}
		}
	}
}

func (s *Session) dispatchEnd(resp ControlResponse) {
	s.dispatchMu.Lock()
	qs := s.curRows
	if qs != nil && qs.id == resp.ID {
		s.curRows = nil
	} else {
		qs = nil
	}
	s.dispatchMu.Unlock()
	if qs != nil {
		if !resp.OK {
			// 中途失败：不能伪装成干净 EOF，把错误带给 Next()
			qs.err = errors.New(resp.Error)
		}
		close(qs.rows)
	}
}

// abortStream 放弃该会话当前在读的流：先置 err 再 close(qs.rows)（channel close 是
// happens-before 边），Next() 将返回该错误而非伪装成 EOF。readLoop 派发与
// exec/Query 并发，读 curRows 必须持 dispatchMu（与 dispatchEnd 同一纪律）。
func (s *Session) abortStream(err error) {
	s.dispatchMu.Lock()
	qs := s.curRows
	if qs != nil {
		s.curRows = nil
	}
	s.dispatchMu.Unlock()
	if qs != nil {
		qs.err = err
		close(qs.rows)
	}
}

func (s *Session) Close() error {
	_, err := s.exec(context.Background(), ControlRequest{Op: "CLOSE"}, nil)
	s.proc.mu.Lock()
	delete(s.proc.sessions, s.connID)
	s.proc.mu.Unlock()
	return err
}

// QueryStream 实现。
func (q *QueryStream) Cols() []ColMeta { return q.cols }
func (q *QueryStream) Next() ([]any, error) {
	row, ok := <-q.rows
	if !ok {
		if q.err != nil {
			return nil, q.err
		}
		return nil, io.EOF
	}
	return row, nil
}
func (q *QueryStream) Close() error {
	q.once.Do(func() { close(q.done) })
	return nil
}
