package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type pendingInput struct {
	RequestInfo
	NativeID json.RawMessage
	Params   json.RawMessage
}

type observation struct {
	Skip           bool
	Started        bool
	Completed      bool
	State          string
	Error          string
	AssistantEnded bool
	NativeTurnID   string
	ThreadID       string
	Input          *pendingInput
	ResolvedID     json.RawMessage
	UnsupportedID  json.RawMessage
}

type sendResult struct {
	NativeID string
	Handled  bool
}

type adapter interface {
	arguments([]string) ([]string, error)
	initialize(context.Context, *peer, string, NativeSession) (NativeSession, error)
	send(context.Context, *peer, NativeSession, string) (sendResult, error)
	interrupt(context.Context, *peer, NativeSession, *Turn) error
	observe(json.RawMessage) observation
	respond(*peer, pendingInput, json.RawMessage) error
	cancel(pendingInput) json.RawMessage
}

func selectAdapter(name string) (adapter, error) {
	switch name {
	case "pi":
		return piAdapter{}, nil
	case "codex":
		return codexAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported session adapter %q", name)
	}
}

func rejectOwnedArgs(args []string, forbidden ...string) error {
	for _, arg := range args {
		for _, option := range forbidden {
			if arg == option || strings.HasPrefix(arg, option+"=") {
				return fmt.Errorf("session argument %q conflicts with lifecycle control", option)
			}
		}
	}
	return nil
}

func input(method string, id, params json.RawMessage, timeout time.Duration) *pendingInput {
	if timeout <= 0 || timeout > inputTimeout {
		timeout = inputTimeout
	}
	return &pendingInput{RequestInfo: RequestInfo{Method: method, ExpiresAt: time.Now().Add(timeout)}, NativeID: id, Params: params}
}

func responseObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return nil, fmt.Errorf("response must be a JSON object")
	}
	return result, nil
}
