package owljdbc

import (
	"context"
	"io"
	"os/exec"
	"syscall"
	"testing"
)

// killProcGroup 杀掉整个进程组。`go run` 会派生一个真正的子进程，
// 仅杀 wrapper（cmd.Process）不会关闭子进程持有的 stdout 管道，
// 因此必须按进程组 SIGKILL，readLoop 才能读到 EOF 并标记进程死亡。
func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

func newTestProc(t *testing.T) *AgentProc {
	t.Helper()
	cmd := exec.Command("go", "run", "./testdata/fake_agent")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
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
	t.Cleanup(func() { killProcGroup(cmd) })
	return p
}

func TestSessionExec(t *testing.T) {
	proc := newTestProc(t)
	s, err := proc.NewSession(Config{Family: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.Exec(context.Background(), ControlRequest{Op: "EXEC", SQL: "INSERT INTO t VALUES(?,?)", Family: "mysql"}, []any{int64(1), "x"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Affected != 3 {
		t.Fatalf("affected = %d", resp.Affected)
	}
}

func TestSessionQuery(t *testing.T) {
	proc := newTestProc(t)
	s, err := proc.NewSession(Config{Family: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	qs, err := s.Query(context.Background(), ControlRequest{Op: "QUERY", SQL: "SELECT 1", Family: "mysql"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs.Cols()) != 1 {
		t.Fatalf("cols = %d", len(qs.Cols()))
	}
	row, err := qs.Next()
	if err != nil {
		t.Fatal(err)
	}
	if row[0] != "hello" {
		t.Fatalf("row[0] = %v", row[0])
	}
	if _, err := qs.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestQueryMidStreamError(t *testing.T) {
	proc := newTestProc(t)
	s, err := proc.NewSession(Config{Family: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	qs, err := s.Query(context.Background(), ControlRequest{Op: "QUERY", SQL: "SELECT __MIDSTREAM_FAIL__", Family: "mysql"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	row, err := qs.Next()
	if err != nil {
		t.Fatalf("first row: %v", err)
	}
	if row[0] != "hello" {
		t.Fatalf("row[0] = %v", row[0])
	}
	// 第二次 Next 必须拿到 END 携带的错误，而不是伪装成干净 EOF
	_, err = qs.Next()
	if err == nil || err == io.EOF || err.Error() != "boom" {
		t.Fatalf("expected mid-stream error boom, got %v", err)
	}
}

func TestSessionProcessDeath(t *testing.T) {
	proc := newTestProc(t)
	s, err := proc.NewSession(Config{Family: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	// 杀进程 → 下一次 Exec 应立即报错，不挂起。
	killProcGroup(proc.cmd)
	_, err = s.Exec(context.Background(), ControlRequest{Op: "PING"}, nil)
	if err == nil {
		t.Fatalf("expected error after process death")
	}
}

func TestSessionContextTimeout(t *testing.T) {
	proc := newTestProc(t)
	s, err := proc.NewSession(Config{Family: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已取消
	_, err = s.Exec(ctx, ControlRequest{Op: "PING"}, nil)
	if err == nil {
		t.Fatalf("expected error on cancelled context")
	}
}
