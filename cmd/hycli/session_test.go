package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yy003x/hycli/internal/session"
)

func TestSessionCommandHelpAndProfileValidation(t *testing.T) {
	home := setup(t, map[string]any{"command": "/bin/echo"})
	for _, operation := range []string{"open", "list", "status", "send", "attach", "respond", "interrupt", "close", "resume"} {
		out, err := command(filepath.Join(home, "missing"), "session", operation, "--help").CombinedOutput()
		if err != nil || !bytes.Contains(out, []byte("Usage: hycli session "+operation)) {
			t.Fatalf("%v: %s", err, out)
		}
	}
	for _, args := range [][]string{{"session", "open", "tool"}, {"session", "send", "not-a-session", "hello"}, {"session", "respond", "id", "request", "not-json"}, {"session", "attach", "--after", "bad", "id"}} {
		if out, err := command(home, args...).CombinedOutput(); err == nil {
			t.Fatalf("unexpected success: %s", out)
		}
	}
	for _, adapter := range []string{"claude", "", "unknown"} {
		home := setup(t, map[string]any{"command": "/bin/echo", "session": map[string]any{"adapter": adapter}})
		if out, err := command(home, "profile", "check", "tool").CombinedOutput(); err == nil {
			t.Fatalf("invalid adapter accepted: %s", out)
		}
	}
}

func TestSessionRealPiInput(t *testing.T) {
	if os.Getenv("HYCLI_SESSION_INTEGRATION") != "1" {
		t.Skip("requires installed Pi")
	}
	demo := os.Getenv("HYCLI_PI_RPC_DEMO")
	if demo == "" {
		t.Skip("set HYCLI_PI_RPC_DEMO to Pi's supplied rpc-demo.ts extension")
	}
	binary, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	home := setup(t, map[string]any{
		"command": binary, "cwd": t.TempDir(),
		"env":     map[string]string{"PATH": os.Getenv("PATH"), "PI_CODING_AGENT_DIR": "${HOME}/pi", "PI_OFFLINE": "1", "PI_TELEMETRY": "0"},
		"session": map[string]any{"adapter": "pi", "args": []string{"--offline", "--no-extensions", "--no-mcp", "--no-skills", "--no-context-files", "-e", demo}},
	})
	out, err := command(home, "session", "open", "tool").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var reply session.Reply
	if err := json.Unmarshal(out, &reply); err != nil {
		t.Fatal(err)
	}
	id := reply.Session.ID
	t.Cleanup(func() {
		if _, err := session.Do(context.Background(), home, id, session.Request{Method: "close"}); err != nil {
			t.Error(err)
		}
		checkWorkerRaceLog(t, home)
	})
	for _, response := range []string{`{"value":"rpc-roundtrip"}`, `{"cancelled":true}`} {
		var output bytes.Buffer
		sendCtx, stopSend := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopSend()
		inputReady := make(chan struct{}, 1)
		done := make(chan error, 1)
		go func() {
			done <- session.Stream(sendCtx, home, id, session.Request{Method: "send", Prompt: "/rpc-input", Wait: true}, func(reply session.Reply) error {
				if reply.Event != nil && reply.Event.Type == "input.required" {
					inputReady <- struct{}{}
				}
				return json.NewEncoder(&output).Encode(reply)
			})
		}()
		select {
		case <-inputReady:
		case <-time.After(10 * time.Second):
			stopSend()
			<-done
			t.Fatal("input request must stream before native prompt acknowledgement")
		}
		state, err := session.Status(context.Background(), home, id)
		if err != nil {
			t.Fatal(err)
		}
		if state.State != "waiting_input" || len(state.Pending) != 1 {
			t.Fatalf("input was not exposed: %+v", state)
		}
		if _, err := session.Do(context.Background(), home, id, session.Request{Method: "send", Prompt: "must not be queued"}); err == nil {
			t.Fatal("busy session accepted a second prompt")
		}
		watchCtx, cancel := context.WithCancel(context.Background())
		_ = session.Stream(watchCtx, home, id, session.Request{Method: "attach"}, func(reply session.Reply) error { cancel(); return nil })
		state, err = session.Status(context.Background(), home, id)
		if err != nil || state.State != "waiting_input" {
			t.Fatalf("observer detach changed task: %+v %v", state, err)
		}
		if _, err := session.Do(context.Background(), home, id, session.Request{Method: "respond", RequestID: state.Pending[0].ID, Response: json.RawMessage(`{"confirmed":true}`)}); err == nil {
			t.Fatal("invalid input response accepted")
		}
		out, err := command(home, "session", "respond", id, state.Pending[0].ID, response).CombinedOutput()
		if err != nil {
			t.Fatalf("respond: %v: %s", err, out)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("send: %v: %s", err, &output)
			}
		case <-time.After(10 * time.Second):
			stopSend()
			<-done
			t.Fatal("prompt did not settle after input response")
		}
		if !bytes.Contains(output.Bytes(), []byte(`"state":"handled"`)) {
			t.Fatalf("extension command was not marked handled: %s", &output)
		}
		if _, err := session.Do(context.Background(), home, id, session.Request{Method: "respond", RequestID: state.Pending[0].ID, Response: json.RawMessage(response)}); err == nil {
			t.Fatal("stale input response accepted")
		}
	}
}

// 显式启用后只使用真实 Pi/Codex 的初始化与生命周期接口，不发送模型任务。
func TestSessionRealProtocolLifecycle(t *testing.T) {
	if os.Getenv("HYCLI_SESSION_INTEGRATION") != "1" {
		t.Skip("set HYCLI_SESSION_INTEGRATION=1 to test installed Pi and Codex")
	}
	for _, adapter := range []string{"pi", "codex"} {
		t.Run(adapter, func(t *testing.T) {
			binary, err := exec.LookPath(adapter)
			if err != nil {
				t.Fatal(err)
			}
			args := []string{}
			env := map[string]string{"PATH": os.Getenv("PATH")}
			if adapter == "pi" {
				args = []string{"--offline", "--no-extensions", "--no-mcp", "--no-skills", "--no-context-files"}
				env["PI_CODING_AGENT_DIR"] = "${HOME}/pi"
				env["PI_OFFLINE"] = "1"
				env["PI_TELEMETRY"] = "0"
			} else {
				env["CODEX_HOME"] = "${HOME}/codex"
			}
			home := setup(t, map[string]any{"command": binary, "env": env, "cwd": t.TempDir(), "session": map[string]any{"adapter": adapter, "args": args}})
			if adapter == "codex" {
				if err := os.Mkdir(filepath.Join(home, "codex"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				out, err := command(home, "session", "list").Output()
				if err != nil {
					t.Errorf("list sessions for cleanup: %v", err)
					return
				}
				var list struct {
					Sessions []session.Snapshot `json:"sessions"`
				}
				if err := json.Unmarshal(out, &list); err != nil {
					t.Error(err)
					return
				}
				for _, state := range list.Sessions {
					if out, err := command(home, "session", "close", state.ID).CombinedOutput(); err != nil {
						t.Errorf("close %s: %v: %s", state.ID, err, out)
					}
				}
				checkWorkerRaceLog(t, home)
			})
			out, err := command(home, "session", "open", "tool").CombinedOutput()
			if err != nil {
				t.Errorf("open: %v: %s", err, out)
				logs, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*.log"))
				for _, path := range logs {
					data, _ := os.ReadFile(path)
					t.Logf("%s: %s", filepath.Base(path), data)
				}
				return
			}
			var opened session.Reply
			if err := json.Unmarshal(out, &opened); err != nil {
				t.Fatal(err)
			}
			if !opened.OK || opened.Session == nil || opened.Session.State != "idle" || opened.Session.Native.ID == "" || opened.Session.AgentPID == 0 {
				t.Fatalf("not ready: %s", out)
			}
			state := *opened.Session
			out, err = command(home, "session", "status", state.ID).Output()
			if err != nil {
				t.Fatal(err)
			}
			var current session.Reply
			if err := json.Unmarshal(out, &current); err != nil {
				t.Fatal(err)
			}
			if current.Session.AgentPID != state.AgentPID || current.Session.Instance != state.Instance || current.Session.Native.ID != state.Native.ID {
				t.Fatalf("process/session was replaced: %s", out)
			}
			if out, err := command(home, "session", "interrupt", state.ID).CombinedOutput(); err == nil || !bytes.Contains(out, []byte("no active turn")) {
				t.Fatalf("idle interrupt: %v: %s", err, out)
			}
			out, err = command(home, "session", "close", state.ID).Output()
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out, &current); err != nil {
				t.Fatal(err)
			}
			if current.Session.State != "closed" {
				t.Fatalf("close did not settle: %s", out)
			}
			if out, err := command(home, "session", "close", state.ID).Output(); err != nil {
				t.Fatalf("repeat close: %v: %s", err, out)
			}
		})
	}
}

func checkWorkerRaceLog(t *testing.T, home string) {
	t.Helper()
	logs, err := filepath.Glob(filepath.Join(home, "sessions", "*", "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range logs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Error(err)
			continue
		}
		if bytes.Contains(data, []byte("WARNING: DATA RACE")) {
			t.Errorf("worker race: %s", data)
		}
	}
}
