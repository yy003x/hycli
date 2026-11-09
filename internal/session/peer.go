package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type wireMessage struct {
	ID      json.RawMessage `json:"id"`
	Type    string          `json:"type"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Data    json.RawMessage `json:"data"`
	Error   json.RawMessage `json:"error"`
	Success *bool           `json:"success"`
}

type writeError struct{ error }

type peer struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	writeMu  sync.Mutex
	mu       sync.Mutex
	waiters  map[string]chan wireMessage
	next     atomic.Uint64
	done     chan struct{}
	readDone chan struct{}
}

func startPeer(command string, args, env []string, cwd string, stderr io.Writer, onMessage func(json.RawMessage), onError func(error)) (*peer, error) {
	p := &peer{waiters: map[string]chan wireMessage{}, done: make(chan struct{}), readDone: make(chan struct{})}
	p.cmd = exec.Command(command, args...)
	p.cmd.Env, p.cmd.Dir, p.cmd.Stderr = env, cwd, stderr
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var err error
	if p.stdin, err = p.cmd.StdinPipe(); err != nil {
		return nil, err
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		p.stdin.Close()
		return nil, err
	}
	if err = p.cmd.Start(); err != nil {
		p.stdin.Close()
		stdout.Close()
		return nil, err
	}
	go func() {
		defer close(p.readDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), maxFrame)
		for scanner.Scan() {
			raw := append(json.RawMessage(nil), scanner.Bytes()...)
			var msg wireMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				onError(fmt.Errorf("invalid agent protocol JSON: %w", err))
				return
			}
			if len(msg.ID) != 0 && string(msg.ID) != "null" && (msg.Type == "response" || (msg.Type == "" && msg.Method == "")) {
				p.mu.Lock()
				waiter := p.waiters[string(msg.ID)]
				delete(p.waiters, string(msg.ID))
				p.mu.Unlock()
				if waiter != nil {
					waiter <- msg
				}
				continue
			}
			onMessage(raw)
		}
		if err := scanner.Err(); err != nil {
			onError(fmt.Errorf("read agent protocol: %w", err))
		} else {
			onError(errors.New("agent protocol stream closed"))
		}
	}()
	go func() {
		// 先排空协议管道，再 Wait，避免最后一个完成事件被提前关闭的 stdout 截断。
		<-p.readDone
		err := p.cmd.Wait()
		close(p.done)
		if err == nil {
			err = errors.New("agent process exited")
		}
		onError(err)
	}()
	return p, nil
}

func (p *peer) write(value any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if f, ok := p.stdin.(*os.File); ok {
		_ = f.SetWriteDeadline(time.Now().Add(5 * time.Second))
	}
	if err := json.NewEncoder(p.stdin).Encode(value); err != nil {
		return writeError{err}
	}
	return nil
}

func (p *peer) call(ctx context.Context, request map[string]any) (json.RawMessage, error) {
	id := fmt.Sprintf("hy-%d", p.next.Add(1))
	key, _ := json.Marshal(id)
	waiter := make(chan wireMessage, 1)
	p.mu.Lock()
	p.waiters[string(key)] = waiter
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.waiters, string(key)); p.mu.Unlock() }()
	request["id"] = id
	if err := p.write(request); err != nil {
		return nil, err
	}
	select {
	case msg := <-waiter:
		if (len(msg.Error) > 0 && string(msg.Error) != "null") || (msg.Success != nil && !*msg.Success) {
			return nil, fmt.Errorf("agent rejected request: %s", msg.Error)
		}
		if msg.Type == "response" {
			return msg.Data, nil
		}
		return msg.Result, nil
	case <-p.done:
		return nil, errors.New("agent exited before responding")
	case <-ctx.Done():
		return nil, fmt.Errorf("agent response unavailable (request must not be replayed automatically): %w", ctx.Err())
	}
}

func (p *peer) stop() error {
	p.writeMu.Lock()
	_ = p.stdin.Close()
	p.writeMu.Unlock()
	select {
	case <-p.done:
		return nil
	case <-time.After(2 * time.Second):
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-p.done:
		return nil
	case <-time.After(2 * time.Second):
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-p.done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("owned agent did not exit after SIGKILL; process cleanup is incomplete")
	}
}
