package owljdbc

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// fakeAgentBinary 构建一次 fake sidecar 二进制并复用。直接 exec 该二进制
// （而非 `go run`）使 cmd.Process 就是真正的 agent，Kill 它即可关闭 stdout
// 管道让 readLoop 读到 EOF——无需进程组，macOS/Linux/Windows 行为一致。
var (
	fakeAgentOnce sync.Once
	fakeAgentPath string
	fakeAgentErr  error
)

func fakeAgentBinary(t *testing.T) string {
	t.Helper()
	fakeAgentOnce.Do(func() {
		dir, err := os.MkdirTemp("", "owljdbc-fake-agent")
		if err != nil {
			fakeAgentErr = err
			return
		}
		bin := filepath.Join(dir, "fake_agent")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", bin, "./testdata/fake_agent")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fakeAgentErr = err
			return
		}
		fakeAgentPath = bin
	})
	if fakeAgentErr != nil {
		t.Fatalf("build fake agent: %v", fakeAgentErr)
	}
	return fakeAgentPath
}

func killProc(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func newTestProc(t *testing.T) *AgentProc {
	t.Helper()
	cmd := exec.Command(fakeAgentBinary(t))
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
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { killProc(cmd) })
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
	killProc(proc.cmd)
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
