package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yy003x/hycli/internal/launch"
	"github.com/yy003x/hycli/internal/profile"
)

func Open(ctx context.Context, home, profileID, cwd string) (Snapshot, error) {
	s, err := newStore(home)
	if err != nil {
		return Snapshot{}, err
	}
	p, err := profile.Load(filepath.Join(s.home, "configs"), profileID)
	if err != nil {
		return Snapshot{}, err
	}
	if p.Session == nil {
		return Snapshot{}, fmt.Errorf("profile %s does not define session.adapter and session.args", profileID)
	}
	driver, err := selectAdapter(p.Session.Adapter)
	if err != nil {
		return Snapshot{}, err
	}
	if _, err = driver.arguments(p.Session.Args); err != nil {
		return Snapshot{}, err
	}
	if cwd != "" {
		p.Cwd = cwd
	}
	_, cwd, _, err = launch.Prepare(p)
	if err != nil {
		return Snapshot{}, err
	}
	id, err := randomID("s_")
	if err != nil {
		return Snapshot{}, err
	}
	if err = s.create(id); err != nil {
		return Snapshot{}, err
	}
	state := Snapshot{SchemaVersion: protocolVersion, ID: id, Profile: profileID, Adapter: p.Session.Adapter, CWD: cwd, CreatedAt: time.Now().UTC()}
	return s.start(ctx, state)
}

func Resume(ctx context.Context, home, id string) (Snapshot, error) {
	s, err := newStore(home)
	if err != nil {
		return Snapshot{}, err
	}
	state, err := s.load(id)
	if err != nil {
		return Snapshot{}, err
	}
	if state.Native.ID == "" {
		return state, fmt.Errorf("session has no native identity to resume")
	}
	if state.Adapter == "pi" {
		if _, err := os.Stat(state.Native.Path); err != nil {
			return state, fmt.Errorf("Pi has not persisted a resumable session: %w", err)
		}
	}
	return s.start(ctx, state)
}

func (s store) start(ctx context.Context, state Snapshot) (Snapshot, error) {
	lease, err := s.lock(state.ID)
	if err != nil {
		return state, err
	}
	defer lease.Close()
	if state.Instance != "" {
		state, err = s.load(state.ID)
		if err != nil {
			return state, err
		}
	}
	state.Instance, err = randomID("")
	if err != nil {
		return state, err
	}
	state.State, state.Error, state.WorkerPID, state.AgentPID = "starting", "", 0, 0
	state.Pending = nil
	state.UpdatedAt = time.Now().UTC()
	if turnActive(state.Turn) {
		state.Turn.State, state.Turn.Error, state.Turn.CompletedAt = "unknown", "previous worker stopped without a terminal result; no prompt was replayed", time.Now().UTC()
	}
	if err = s.save(state); err != nil {
		return state, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return state, err
	}
	defer read.Close()
	defer write.Close()
	executable, err := os.Executable()
	if err != nil {
		return state, err
	}
	dir, _ := s.dir(state.ID)
	log, err := os.OpenFile(filepath.Join(dir, "worker.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return state, err
	}
	defer log.Close()
	cmd := exec.Command(executable, "__session-worker", s.home, state.ID, state.Instance)
	cmd.Dir = state.CWD
	cmd.Stdout, cmd.Stderr = log, log
	cmd.ExtraFiles = []*os.File{write, lease}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		state.State, state.Error = "failed", err.Error()
		_ = s.save(state)
		return state, err
	}
	_ = write.Close()
	type readiness struct {
		reply Reply
		err   error
	}
	ready := make(chan readiness, 1)
	go func() {
		var reply Reply
		err := json.NewDecoder(io.LimitReader(read, maxFrame)).Decode(&reply)
		ready <- readiness{reply, err}
	}()
	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout+5*time.Second)
	defer cancel()
	select {
	case result := <-ready:
		if result.err == nil && result.reply.OK && result.reply.Session != nil {
			_ = cmd.Process.Release()
			return *result.reply.Session, nil
		}
		err = result.err
		if err == nil {
			err = errors.New(result.reply.Error)
		}
	case <-startupCtx.Done():
		err = startupCtx.Err()
	}
	// 只终止本次创建且仍持有进程句柄的 worker；不根据落盘 PID 猜测进程身份。
	_ = cmd.Process.Signal(syscall.SIGTERM)
	stopped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		<-stopped
	}
	if current, loadErr := s.load(state.ID); loadErr == nil {
		state = current
	}
	return state, fmt.Errorf("session %s failed to open: %w (diagnostics: %s)", state.ID, err, dir)
}

func connect(ctx context.Context, s store, state Snapshot, request Request) (net.Conn, error) {
	path, err := s.socket(state)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	request.Version, request.Instance = protocolVersion, state.Instance
	if err = conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err == nil {
		err = json.NewEncoder(conn).Encode(request)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func Do(ctx context.Context, home, id string, request Request) (Reply, error) {
	s, err := newStore(home)
	if err != nil {
		return Reply{}, err
	}
	state, err := s.load(id)
	if err != nil {
		return Reply{}, err
	}
	if request.Method == "close" && terminal(state.State) {
		return Reply{OK: true, Session: &state}, s.waitStopped(ctx, id)
	}
	conn, err := connect(ctx, s, state, request)
	if err != nil {
		return Reply{}, fmt.Errorf("session %s worker is unreachable: %w", id, err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(requestTimeout + 10*time.Second))
	var reply Reply
	err = json.NewDecoder(io.LimitReader(conn, maxFrame)).Decode(&reply)
	if err != nil {
		return reply, err
	}
	if !reply.OK {
		return reply, errors.New(reply.Error)
	}
	if request.Method == "close" {
		return reply, s.waitStopped(ctx, id)
	}
	return reply, nil
}

func (s store) waitStopped(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for {
		lease, err := s.lock(id)
		if err == nil {
			return lease.Close()
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("worker has not released its lease: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func Status(ctx context.Context, home, id string) (Snapshot, error) {
	s, err := newStore(home)
	if err != nil {
		return Snapshot{}, err
	}
	state, err := s.load(id)
	if err != nil {
		return state, err
	}
	if terminal(state.State) {
		return state, nil
	}
	reply, err := Do(ctx, home, id, Request{Method: "status"})
	if err != nil {
		// worker 先关闭监听再收口；失败的连接可能与终态落盘同时发生。
		if current, loadErr := s.load(id); loadErr == nil && terminal(current.State) {
			return current, nil
		}
		state.State, state.Error = "unreachable", err.Error()
		return state, nil
	}
	if reply.Session == nil {
		return state, fmt.Errorf("worker returned no session state")
	}
	return *reply.Session, nil
}

func List(ctx context.Context, home string) ([]Snapshot, error) {
	s, err := newStore(home)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(s.home, "sessions"))
	if os.IsNotExist(err) {
		return []Snapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := []Snapshot{}
	for _, entry := range entries {
		if !entry.IsDir() || !sessionID.MatchString(entry.Name()) {
			continue
		}
		state, err := Status(ctx, home, entry.Name())
		if err != nil {
			return nil, err
		}
		result = append(result, state)
	}
	return result, nil
}

func Stream(ctx context.Context, home, id string, request Request, consume func(Reply) error) error {
	s, err := newStore(home)
	if err != nil {
		return err
	}
	state, err := s.load(id)
	if err != nil {
		return err
	}
	conn, err := connect(ctx, s, state, request)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64<<10), maxFrame*2)
	completed := false
	waitTurnID := ""
	for scanner.Scan() {
		var reply Reply
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
			return err
		}
		if err := consume(reply); err != nil {
			return err
		}
		if !reply.OK {
			return errors.New(reply.Error)
		}
		if request.Wait && waitTurnID == "" && reply.Session != nil && reply.Session.Turn != nil {
			waitTurnID = reply.Session.Turn.ID
		}
		if reply.Event != nil && reply.Event.Type == "turn.completed" {
			if request.Wait && reply.Event.TurnID != waitTurnID {
				continue
			}
			var turn Turn
			if err := json.Unmarshal(reply.Event.Data, &turn); err != nil {
				return err
			}
			completed = true
			if request.Wait && turn.State != "completed" && turn.State != "handled" {
				return fmt.Errorf("turn %s ended as %s: %s", turn.ID, turn.State, turn.Error)
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if request.Wait && !completed {
		return fmt.Errorf("worker disconnected without a terminal turn result; do not replay the prompt automatically")
	}
	return nil
}
