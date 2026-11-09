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
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/yy003x/hycli/internal/launch"
	"github.com/yy003x/hycli/internal/profile"
)

type worker struct {
	mu            sync.Mutex
	store         store
	state         Snapshot
	driver        adapter
	peer          *peer
	inputs        map[string]*pendingInput
	responding    map[string]bool
	events        []Event
	eventBytes    int
	changed       chan struct{}
	fatal         chan error
	stop          chan struct{}
	stopOnce      sync.Once
	finished      chan struct{}
	connections   sync.WaitGroup
	serverDone    chan struct{}
	sending       bool
	nativeRunning bool
	completed     map[string]Event
	completedIDs  []string
}

// RunWorker 仅由 Open/Resume 通过继承的就绪管道和文件锁启动。
func RunWorker(home, id, instance string) error {
	s, err := newStore(home)
	if err != nil {
		return err
	}
	ready, lease := os.NewFile(3, "ready"), os.NewFile(4, "lease")
	if ready == nil || lease == nil {
		return fmt.Errorf("session worker requires inherited descriptors")
	}
	defer ready.Close()
	defer lease.Close()
	info, err := ready.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return fmt.Errorf("invalid worker readiness pipe")
	}
	dir, err := s.dir(id)
	if err != nil {
		return err
	}
	leaseInfo, err := lease.Stat()
	if err != nil {
		return err
	}
	lockInfo, err := os.Stat(filepath.Join(dir, "worker.lock"))
	if err != nil || !os.SameFile(leaseInfo, lockInfo) {
		return fmt.Errorf("invalid worker lease")
	}
	state, err := s.load(id)
	if err != nil {
		return err
	}
	if state.Instance != instance || state.State != "starting" {
		return fmt.Errorf("stale worker instance")
	}
	w := &worker{store: s, state: state, inputs: map[string]*pendingInput{}, responding: map[string]bool{}, changed: make(chan struct{}), fatal: make(chan error, 1), stop: make(chan struct{}), finished: make(chan struct{}), serverDone: make(chan struct{}), completed: map[string]Event{}}
	w.state.WorkerPID = os.Getpid()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer cancel()
	listener, err := w.start(ctx)
	if err != nil {
		w.shutdown(err)
		_ = json.NewEncoder(ready).Encode(Reply{Error: err.Error()})
		return err
	}
	defer listener.Close()
	w.mu.Lock()
	snapshot := w.snapshotLocked()
	w.mu.Unlock()
	if err := json.NewEncoder(ready).Encode(Reply{OK: true, Session: &snapshot}); err != nil {
		w.shutdown(err)
		return err
	}
	_ = ready.Close()
	select {
	case err = <-w.fatal:
	case <-ctx.Done():
	case <-w.stop:
	}
	_ = listener.Close()
	<-w.serverDone
	w.shutdown(err)
	// close 的应答和观察端的最后一个事件必须先送出，再退出 worker。
	done := make(chan struct{})
	go func() { w.connections.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
	}
	return err
}

func (w *worker) start(ctx context.Context) (net.Listener, error) {
	p, err := profile.Load(filepath.Join(w.store.home, "configs"), w.state.Profile)
	if err != nil {
		return nil, err
	}
	if p.Session == nil || p.Session.Adapter != w.state.Adapter {
		return nil, fmt.Errorf("profile session adapter changed or is missing")
	}
	w.driver, err = selectAdapter(p.Session.Adapter)
	if err != nil {
		return nil, err
	}
	args, err := w.driver.arguments(p.Session.Args)
	if err != nil {
		return nil, err
	}
	// worker 已在确定的工作目录启动，不能再次展开已解析路径中的 ${...}。
	p.Cwd = ""
	command, cwd, env, err := launch.Prepare(p)
	if err != nil {
		return nil, err
	}
	path, err := w.store.socket(w.state)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	go w.serve(listener)
	dir, _ := w.store.dir(w.state.ID)
	log, err := os.OpenFile(filepath.Join(dir, "agent.stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		listener.Close()
		return nil, err
	}
	peer, err := startPeer(command, args, env, cwd, &limitedLog{file: log}, w.onMessage, w.fail)
	if err != nil {
		log.Close()
		listener.Close()
		return nil, err
	}
	go func() { <-peer.done; log.Close() }()
	w.mu.Lock()
	w.peer = peer
	w.state.AgentPID = peer.cmd.Process.Pid
	previous := w.state.Native
	err = w.saveLocked()
	w.mu.Unlock()
	if err != nil {
		listener.Close()
		return nil, err
	}
	initCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	type initialized struct {
		native NativeSession
		err    error
	}
	result := make(chan initialized, 1)
	go func() {
		native, err := w.driver.initialize(initCtx, peer, cwd, previous)
		result <- initialized{native, err}
	}()
	var initializedResult initialized
	select {
	case initializedResult = <-result:
	case err = <-w.fatal:
	case <-initCtx.Done():
		err = initCtx.Err()
	case <-w.stop:
		err = errors.New("session closed before initialization")
	}
	if err == nil {
		err = initializedResult.err
	}
	if err != nil {
		listener.Close()
		return nil, err
	}
	w.mu.Lock()
	w.state.Native, w.state.State, w.state.Error = initializedResult.native, "idle", ""
	w.emitLocked("session.ready", "", initializedResult.native)
	err = w.saveLocked()
	w.mu.Unlock()
	if err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

// stderr 持续排空但限制落盘体积，不把协议输出或提示词写入控制元数据。
type limitedLog struct {
	file    *os.File
	written int
}

func (l *limitedLog) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (1 << 20) - l.written
	if remaining > 0 {
		part := p
		if len(part) > remaining {
			part = part[:remaining]
		}
		written, err := l.file.Write(part)
		l.written += written
		if err != nil {
			return written, err
		}
	}
	return n, nil
}

func (w *worker) fail(err error) {
	select {
	case w.fatal <- err:
	default:
	}
}

func (w *worker) snapshotLocked() Snapshot {
	result := w.state
	if result.Turn != nil {
		value := *result.Turn
		result.Turn = &value
	}
	result.Pending = nil
	for _, request := range w.inputs {
		info := request.RequestInfo
		info.Parameters = request.Params
		result.Pending = append(result.Pending, info)
	}
	sort.Slice(result.Pending, func(i, j int) bool { return result.Pending[i].ID < result.Pending[j].ID })
	return result
}

func (w *worker) saveLocked() error {
	w.state.UpdatedAt = time.Now().UTC()
	err := w.store.save(w.snapshotLocked())
	if err != nil {
		w.fail(fmt.Errorf("persist session control state: %w", err))
	}
	return err
}

func (w *worker) emitLocked(kind, turn string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		w.fail(err)
		return
	}
	w.state.Sequence++
	w.events = append(w.events, Event{Sequence: w.state.Sequence, Time: time.Now().UTC(), Type: kind, TurnID: turn, Data: raw})
	w.eventBytes += len(raw)
	for len(w.events) > 512 || (w.eventBytes > 32<<20 && len(w.events) > 1) {
		w.eventBytes -= len(w.events[0].Data)
		w.events[0] = Event{}
		w.events = w.events[1:]
	}
	close(w.changed)
	w.changed = make(chan struct{})
}

func (w *worker) onMessage(raw json.RawMessage) {
	observed := w.driver.observe(raw)
	if observed.Skip {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	turnID := ""
	if turnActive(w.state.Turn) {
		turnID = w.state.Turn.ID
	}
	w.emitLocked("agent.event", turnID, raw)
	if w.state.State == "closing" || terminal(w.state.State) {
		return
	}
	if observed.ThreadID != "" && w.state.Native.ID != "" && observed.ThreadID != w.state.Native.ID {
		return
	}
	if len(observed.UnsupportedID) > 0 {
		if w.peer == nil {
			w.fail(errors.New("unsupported agent request during startup"))
			return
		}
		peer := w.peer
		go func() {
			if err := peer.write(map[string]any{"id": observed.UnsupportedID, "error": map[string]any{"code": -32601, "message": "hycli does not support this client request"}}); err != nil {
				w.fail(err)
			}
		}()
	}
	if observed.Input != nil {
		if w.state.State == "starting" {
			w.fail(errors.New("agent requested interactive input during startup; configure it using its own CLI first"))
			return
		}
		id, err := randomID("q_")
		if err != nil {
			w.fail(err)
			return
		}
		observed.Input.ID = id
		w.inputs[id] = observed.Input
		w.state.State = "waiting_input"
		w.emitLocked("input.required", turnID, map[string]any{"request": observed.Input.RequestInfo, "params": observed.Input.Params})
		_ = w.saveLocked()
		go w.expireInput(id, observed.Input.ExpiresAt)
	}
	if len(observed.ResolvedID) > 0 {
		for id, request := range w.inputs {
			if string(request.NativeID) == string(observed.ResolvedID) {
				delete(w.inputs, id)
				delete(w.responding, id)
				w.emitLocked("input.resolved", turnID, request.RequestInfo)
			}
		}
		w.refreshStateLocked()
		_ = w.saveLocked()
	}
	if observed.Started {
		w.nativeRunning = true
		if !turnActive(w.state.Turn) && w.state.State != "starting" {
			// 扩展可自行启动后续工作；单独跟踪，不能让下一次 send 与其交错。
			id, err := randomID("t_")
			if err != nil {
				w.fail(err)
				return
			}
			w.state.Turn = &Turn{ID: id, Accepted: true, State: "running", StartedAt: time.Now().UTC()}
			w.emitLocked("turn.accepted", id, *w.state.Turn)
		}
		w.refreshStateLocked()
		_ = w.saveLocked()
	}
	if observed.Completed {
		w.nativeRunning = false
	}
	if !turnActive(w.state.Turn) {
		return
	}
	if observed.NativeTurnID != "" {
		if w.state.Turn.NativeID != "" && w.state.Turn.NativeID != observed.NativeTurnID {
			return
		}
		w.state.Turn.NativeID = observed.NativeTurnID
	}
	if observed.AssistantEnded {
		w.state.Turn.Error = observed.Error
	}
	if observed.Completed {
		state, err := observed.State, observed.Error
		if state == "completed" && w.state.Turn.Error != "" {
			state, err = "failed", w.state.Turn.Error
		}
		w.completeLocked(state, err)
	}
}

func (w *worker) refreshStateLocked() {
	if terminal(w.state.State) || w.state.State == "closing" || w.state.State == "starting" {
		return
	}
	w.state.State = "idle"
	if turnActive(w.state.Turn) {
		w.state.State = w.state.Turn.State
	}
	if len(w.inputs) > 0 {
		w.state.State = "waiting_input"
	}
}

func (w *worker) completeLocked(state, message string) {
	if !turnActive(w.state.Turn) {
		return
	}
	w.state.Turn.State, w.state.Turn.Error, w.state.Turn.CompletedAt = state, message, time.Now().UTC()
	w.inputs = map[string]*pendingInput{}
	w.responding = map[string]bool{}
	w.refreshStateLocked()
	w.emitLocked("turn.completed", w.state.Turn.ID, *w.state.Turn)
	w.completed[w.state.Turn.ID] = w.events[len(w.events)-1]
	w.completedIDs = append(w.completedIDs, w.state.Turn.ID)
	if len(w.completedIDs) > 32 {
		delete(w.completed, w.completedIDs[0])
		w.completedIDs = w.completedIDs[1:]
	}
	_ = w.saveLocked()
}

func (w *worker) send(prompt string) (Snapshot, uint64, error) {
	w.mu.Lock()
	if w.state.State != "idle" || w.sending {
		state := w.state.State
		if w.sending && state == "idle" {
			state = "awaiting agent acknowledgement"
		}
		w.mu.Unlock()
		return Snapshot{}, 0, fmt.Errorf("session is %s; concurrent sends are not queued", state)
	}
	id, err := randomID("t_")
	if err != nil {
		w.mu.Unlock()
		return Snapshot{}, 0, err
	}
	after := w.state.Sequence
	w.sending = true
	w.state.Turn = &Turn{ID: id, State: "submitted", StartedAt: time.Now().UTC()}
	w.state.State = "submitted"
	w.emitLocked("turn.submitted", id, *w.state.Turn)
	if err := w.saveLocked(); err != nil {
		w.sending = false
		w.mu.Unlock()
		return Snapshot{}, after, err
	}
	native, peer, snapshot := w.state.Native, w.peer, w.snapshotLocked()
	w.mu.Unlock()
	// 原生扩展可能在 prompt 应答前等待 UI，先返回控制端接收凭据并立即开放事件流。
	go w.deliver(id, peer, native, prompt)
	return snapshot, after, nil
}

func (w *worker) deliver(id string, peer *peer, native NativeSession, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), inputTimeout+requestTimeout)
	defer cancel()
	result, err := w.driver.send(ctx, peer, native, prompt)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sending = false
	if w.state.State == "closing" || terminal(w.state.State) {
		return
	}
	if err != nil {
		w.completeLocked("failed", err.Error())
		w.fail(err)
		return
	}
	if w.state.Turn != nil && w.state.Turn.ID == id {
		w.state.Turn.Accepted = true
		if w.state.Turn.State == "submitted" {
			w.state.Turn.State = "running"
			w.refreshStateLocked()
		}
		if result.NativeID != "" {
			w.state.Turn.NativeID = result.NativeID
		}
		w.emitLocked("turn.accepted", id, *w.state.Turn)
		if result.Handled && !w.nativeRunning {
			state := "handled"
			if w.state.Turn.State == "interrupting" {
				state = "interrupted"
			}
			w.completeLocked(state, "")
		}
	}
	_ = w.saveLocked()
}

func (w *worker) respond(id string, response json.RawMessage) error {
	w.mu.Lock()
	request := w.inputs[id]
	if request == nil || w.responding[id] {
		w.mu.Unlock()
		return fmt.Errorf("request %s is no longer pending", id)
	}
	w.responding[id] = true
	value, peer := *request, w.peer
	w.mu.Unlock()
	err := w.driver.respond(peer, value, response)
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.responding, id)
	if err != nil {
		var transport writeError
		if errors.As(err, &transport) {
			w.fail(fmt.Errorf("input response delivery is uncertain; not replaying: %w", err))
		}
		return err
	}
	if w.inputs[id] == request {
		delete(w.inputs, id)
		w.emitLocked("input.resolved", "", request.RequestInfo)
		w.refreshStateLocked()
		return w.saveLocked()
	}
	return nil
}

func (w *worker) expireInput(id string, expires time.Time) {
	timer := time.NewTimer(time.Until(expires))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-w.finished:
		return
	}
	w.mu.Lock()
	request := w.inputs[id]
	w.mu.Unlock()
	if request == nil {
		return
	}
	// 无控制端回应时只取消/拒绝，不替用户批准，也不反复重放响应。
	if err := w.respond(id, w.driver.cancel(*request)); err != nil {
		w.mu.Lock()
		pending := w.inputs[id] != nil
		w.mu.Unlock()
		if pending {
			w.fail(fmt.Errorf("cancel expired input: %w", err))
		}
	}
}

func (w *worker) interrupt() error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	w.mu.Lock()
	if !turnActive(w.state.Turn) {
		w.mu.Unlock()
		return fmt.Errorf("session has no active turn")
	}
	id := w.state.Turn.ID
	for (w.state.Adapter == "codex" && w.state.Turn.NativeID == "") || (w.state.Adapter == "pi" && !w.state.Turn.Accepted && len(w.inputs) == 0) {
		changed := w.changed
		w.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("turn did not become interruptible: %w", ctx.Err())
		}
		w.mu.Lock()
		if !turnActive(w.state.Turn) || w.state.Turn.ID != id {
			w.mu.Unlock()
			return fmt.Errorf("turn settled before interruption")
		}
	}
	turn, native, peer := *w.state.Turn, w.state.Native, w.peer
	w.state.Turn.State = "interrupting"
	w.refreshStateLocked()
	inputs := make(map[string]pendingInput, len(w.inputs))
	for id, value := range w.inputs {
		inputs[id] = *value
	}
	err := w.saveLocked()
	w.mu.Unlock()
	if err != nil {
		return err
	}
	for id, request := range inputs {
		if err := w.respond(id, w.driver.cancel(request)); err != nil {
			w.mu.Lock()
			pending := w.inputs[id] != nil
			w.mu.Unlock()
			if pending {
				return err
			}
		}
	}
	return w.driver.interrupt(ctx, peer, native, &turn)
}

func (w *worker) shutdown(cause error) {
	w.mu.Lock()
	w.state.State = "closing"
	peer := w.peer
	w.mu.Unlock()
	if peer != nil {
		if err := peer.stop(); err != nil {
			cause = errors.Join(cause, err)
		}
	}
	w.mu.Lock()
	if turnActive(w.state.Turn) {
		state, message := "interrupted", "session closed"
		if cause != nil {
			state, message = "unknown", cause.Error()
		}
		w.completeLocked(state, message)
	}
	w.inputs = map[string]*pendingInput{}
	w.state.State = "closed"
	if cause != nil {
		w.state.State, w.state.Error = "failed", cause.Error()
	}
	w.emitLocked("session."+w.state.State, "", map[string]string{"error": w.state.Error})
	_ = w.saveLocked()
	close(w.finished)
	w.mu.Unlock()
}

func (w *worker) serve(listener net.Listener) {
	defer close(w.serverDone)
	limit := make(chan struct{}, 32)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		select {
		case limit <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		w.connections.Add(1)
		go func() { defer w.connections.Done(); defer func() { <-limit }(); defer conn.Close(); w.handle(conn) }()
	}
}

func writeReply(conn net.Conn, reply Reply) error {
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return json.NewEncoder(conn).Encode(reply)
}

func (w *worker) handle(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), maxFrame)
	if !scanner.Scan() {
		return
	}
	var request Request
	if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
		_ = writeReply(conn, Reply{Error: err.Error()})
		return
	}
	w.mu.Lock()
	instance := w.state.Instance
	w.mu.Unlock()
	if request.Version != protocolVersion || request.Instance != instance {
		_ = writeReply(conn, Reply{Error: "stale session instance or incompatible control protocol"})
		return
	}
	var err error
	var state Snapshot
	var after uint64
	switch request.Method {
	case "status":
	case "send":
		if request.Prompt == "" {
			err = errors.New("prompt is required")
		} else {
			state, after, err = w.send(request.Prompt)
		}
		if err == nil {
			if writeReply(conn, Reply{OK: true, Session: &state}) != nil {
				return
			}
			if request.Wait {
				w.stream(conn, after, state.Turn.ID)
			}
			return
		}
	case "attach":
		w.stream(conn, request.After, "")
		return
	case "respond":
		err = w.respond(request.RequestID, request.Response)
	case "interrupt":
		err = w.interrupt()
	case "close":
		w.mu.Lock()
		if !terminal(w.state.State) {
			w.state.State = "closing"
		}
		w.mu.Unlock()
		w.stopOnce.Do(func() { close(w.stop) })
		<-w.finished
	default:
		err = fmt.Errorf("unknown session operation %q", request.Method)
	}
	w.mu.Lock()
	state = w.snapshotLocked()
	w.mu.Unlock()
	if err != nil {
		_ = writeReply(conn, Reply{Error: err.Error(), Session: &state})
		return
	}
	_ = writeReply(conn, Reply{OK: true, Session: &state})
}

func (w *worker) stream(conn net.Conn, after uint64, turn string) {
	// 观察端只消费副本，断线或慢读不会阻塞 Agent 的 stdout。
	for {
		w.mu.Lock()
		events := append([]Event(nil), w.events...)
		state := w.snapshotLocked()
		completed, hasCompleted := w.completed[turn]
		changed := w.changed
		w.mu.Unlock()
		if after > state.Sequence {
			_ = writeReply(conn, Reply{Error: "event cursor is ahead of the current session"})
			return
		}
		if len(events) > 0 && after+1 < events[0].Sequence {
			gap := Event{Sequence: events[0].Sequence - 1, Time: time.Now().UTC(), Type: "events.gap", Data: json.RawMessage(`{"reason":"older events are no longer buffered; native history remains with the agent"}`)}
			if writeReply(conn, Reply{OK: true, Event: &gap}) != nil {
				return
			}
			after = gap.Sequence
		}
		for _, event := range events {
			if event.Sequence <= after {
				continue
			}
			if writeReply(conn, Reply{OK: true, Event: &event}) != nil {
				return
			}
			after = event.Sequence
			if turn != "" && event.Type == "turn.completed" && event.TurnID == turn {
				return
			}
		}
		if turn != "" && state.Turn != nil && state.Turn.ID == turn && !turnActive(state.Turn) {
			raw, _ := json.Marshal(state.Turn)
			event := Event{Sequence: state.Sequence, Time: time.Now().UTC(), Type: "turn.completed", TurnID: turn, Data: raw}
			_ = writeReply(conn, Reply{OK: true, Event: &event})
			return
		}
		if turn != "" && hasCompleted {
			completed.Sequence = state.Sequence
			_ = writeReply(conn, Reply{OK: true, Event: &completed})
			return
		}
		if terminal(state.State) {
			return
		}
		select {
		case <-changed:
		case <-time.After(15 * time.Second):
			// 心跳既便于调用方发现断连，也能清理已离开的观察端。
			if writeReply(conn, Reply{OK: true, Session: &state}) != nil {
				return
			}
		}
	}
}

var _ io.Writer = (*limitedLog)(nil)
