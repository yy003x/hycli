package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/yy003x/hycli/internal/session"
)

const sessionUsage = `Usage: hycli session <command>

Manage local agent processes using profiles with a session configuration.

Commands:
  open [--cwd <dir>] <profile>          Start a worker and a new native session
  list                                List managed sessions, including stopped ones
  status <id>                         Inspect process, turn and pending input state
  send [--wait] <id> [--] <prompt>      Send one text prompt; busy sessions reject it
  attach [--after <sequence>] <id>      Replay buffered events and watch live output
  respond <id> <request-id> <json>      Answer an approval or input request
  interrupt <id>                      Interrupt the current turn, keeping the process
  close <id>                          Stop the process and keep native session history
  resume <id>                         Restart a stopped worker using native history

Options must precede the session/profile ID. Use <command> --help for details.
Output is JSON; send --wait and attach stream JSONL control replies/events.
Ctrl-C detaches the observer. Use interrupt to cancel a running turn explicitly.
No task queue, shared daemon, HTTP service, or copied conversation history.
`

func sessionHelp(command string) (string, int) {
	var usage, detail string
	count := 1
	switch command {
	case "open":
		usage = "open [--cwd <dir>] <profile>"
		detail = "Start one worker and one Pi RPC or Codex App Server process. Returns only after native session initialization. session.args is independent of args/interactiveArgs/execArgs."
	case "list":
		usage = "list"
		detail = "List lifecycle metadata and current worker reachability. Does not start agents."
		count = 0
	case "status":
		usage = "status <id>"
		detail = "Inspect current state, native identity, latest turn and pending inputs. A reachable worker includes input parameters even after older events expire. Does not call an LLM."
	case "send":
		usage = "send [--wait] <id> [--] <prompt>"
		detail = "Send exactly one text argument to the existing native session. Return after worker acceptance; turn.accepted confirms native acceptance later. With --wait, stream immediately until the terminal turn event and exit nonzero on failure/interruption. Busy sessions reject new prompts; they are never queued or replayed."
		count = 2
	case "attach":
		usage = "attach [--after <sequence>] <id>"
		detail = "Watch structured events, not the original TUI. A bounded in-memory buffer supports reconnects; events.gap reports missing older output. Ctrl-C detaches without cancelling the turn."
	case "respond":
		usage = "respond <id> <request-id> <json>"
		detail = "Reply to input.required using its hycli request ID and a native response JSON object. Pi confirm: {\"confirmed\":true}; Codex approval: {\"decision\":\"accept\"}. Unanswered requests are cancelled after at most five minutes, never approved automatically."
		count = 3
	case "interrupt":
		usage = "interrupt <id>"
		detail = "Request cancellation of the active turn. Task completion is reported by a terminal event; the agent process remains available."
	case "close":
		usage = "close <id>"
		detail = "Stop the worker and its agent, escalating to process-group signals if needed. Native history and metadata are retained. Does not signal a PID taken from stale metadata."
	case "resume":
		usage = "resume <id>"
		detail = "Start a new process instance with the same native session ID/path. Uses the current profile and original working directory. Does not replay a prompt or resume a partially executed tool."
	default:
		return "", 0
	}
	return "Usage: hycli session " + usage + "\n\n" + detail + "\n\nOptions:\n  --help  Show this help without reading configuration\n", count
}

func parseSession(args []string) (invocation, error) {
	if len(args) == 0 || args[0] == "--help" {
		return invocation{action: actionHelp, help: sessionUsage}, nil
	}
	help, count := sessionHelp(args[0])
	if help == "" {
		return invocation{}, fmt.Errorf("unknown session command %q; see hycli session --help", args[0])
	}
	call := invocation{action: actionSession, sessionCommand: args[0]}
	args = args[1:]
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "--help":
			return invocation{action: actionHelp, help: help}, nil
		case "--wait":
			if call.sessionCommand != "send" {
				return invocation{}, fmt.Errorf("--wait is only supported by session send")
			}
			call.wait = true
		case "--cwd":
			if call.sessionCommand != "open" || len(args) < 2 || args[1] == "" {
				return invocation{}, fmt.Errorf("session open --cwd requires a directory")
			}
			call.cwd = args[1]
			args = args[1:]
		case "--after":
			if call.sessionCommand != "attach" || len(args) < 2 {
				return invocation{}, fmt.Errorf("session attach --after requires a sequence number")
			}
			var err error
			call.after, err = strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				return invocation{}, fmt.Errorf("invalid event sequence: %w", err)
			}
			args = args[1:]
		default:
			return invocation{}, fmt.Errorf("unknown option %q; see hycli session %s --help", args[0], call.sessionCommand)
		}
		args = args[1:]
	}
	if call.sessionCommand == "send" && len(args) > 1 && args[1] == "--" {
		args = append([]string{args[0]}, args[2:]...)
	}
	if len(args) != count {
		return invocation{}, fmt.Errorf("invalid arguments; see hycli session %s --help", call.sessionCommand)
	}
	if count > 0 {
		call.id = args[0]
		if call.id == "" {
			return invocation{}, fmt.Errorf("session/profile ID cannot be empty")
		}
		call.arguments = args[1:]
	}
	if call.sessionCommand == "send" && strings.TrimSpace(call.arguments[0]) == "" {
		return invocation{}, fmt.Errorf("prompt cannot be empty")
	}
	if call.sessionCommand == "respond" && !json.Valid([]byte(call.arguments[1])) {
		return invocation{}, fmt.Errorf("response must be valid JSON")
	}
	return call, nil
}

func runSession(home string, call invocation, stdout io.Writer) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	encoder := json.NewEncoder(stdout)
	var state session.Snapshot
	var err error
	switch call.sessionCommand {
	case "open":
		state, err = session.Open(ctx, home, call.id, call.cwd)
	case "resume":
		state, err = session.Resume(ctx, home, call.id)
	case "status":
		state, err = session.Status(ctx, home, call.id)
	case "list":
		states, err := session.List(ctx, home)
		if err != nil {
			return err
		}
		return encoder.Encode(struct {
			OK       bool               `json:"ok"`
			Sessions []session.Snapshot `json:"sessions"`
		}{true, states})
	case "attach":
		return session.Stream(ctx, home, call.id, session.Request{Method: "attach", After: call.after}, func(reply session.Reply) error { return encoder.Encode(reply) })
	case "send":
		request := session.Request{Method: "send", Prompt: call.arguments[0], Wait: call.wait}
		if call.wait {
			return session.Stream(ctx, home, call.id, request, func(reply session.Reply) error { return encoder.Encode(reply) })
		}
		reply, err := session.Do(ctx, home, call.id, request)
		if err != nil {
			return err
		}
		return encoder.Encode(reply)
	case "respond", "interrupt", "close":
		request := session.Request{Method: call.sessionCommand}
		if call.sessionCommand == "respond" {
			request.RequestID = call.arguments[0]
			request.Response = json.RawMessage(call.arguments[1])
		}
		reply, err := session.Do(ctx, home, call.id, request)
		if err != nil {
			return err
		}
		return encoder.Encode(reply)
	}
	if err != nil {
		return err
	}
	if err := encoder.Encode(session.Reply{OK: true, Session: &state}); err != nil {
		return err
	}
	if state.State == "unreachable" {
		return fmt.Errorf("%s", state.Error)
	}
	return nil
}
