package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type piAdapter struct{}

func (piAdapter) arguments(args []string) ([]string, error) {
	if err := rejectOwnedArgs(args, "--mode", "--print", "-p", "--continue", "-c", "--resume", "-r", "--session", "--session-id", "--fork", "--no-session", "--"); err != nil {
		return nil, err
	}
	return append(append([]string{}, args...), "--mode", "rpc"), nil
}

func piCall(ctx context.Context, p *peer, method string, params map[string]any) (json.RawMessage, error) {
	if params == nil {
		params = map[string]any{}
	}
	params["type"] = method
	return p.call(ctx, params)
}

func (piAdapter) initialize(ctx context.Context, p *peer, cwd string, previous NativeSession) (NativeSession, error) {
	// 新 RPC 进程本身已创建新会话；再次 new_session 会额外触发扩展的清空确认。
	if previous.ID != "" {
		if previous.Path == "" {
			return NativeSession{}, fmt.Errorf("Pi session has no persisted path to resume")
		}
		raw, err := piCall(ctx, p, "switch_session", map[string]any{"sessionPath": previous.Path})
		if err != nil {
			return NativeSession{}, err
		}
		var switched struct {
			Cancelled bool `json:"cancelled"`
		}
		if err := json.Unmarshal(raw, &switched); err != nil {
			return NativeSession{}, err
		}
		if switched.Cancelled {
			return NativeSession{}, fmt.Errorf("Pi cancelled session selection")
		}
	}
	raw, err := piCall(ctx, p, "get_state", nil)
	if err != nil {
		return NativeSession{}, err
	}
	var state struct {
		ID         string `json:"sessionId"`
		File       string `json:"sessionFile"`
		Streaming  bool   `json:"isStreaming"`
		Compacting bool   `json:"isCompacting"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return NativeSession{}, err
	}
	if state.ID == "" || state.Streaming || state.Compacting {
		return NativeSession{}, fmt.Errorf("Pi did not establish an idle native session")
	}
	if previous.ID != "" && previous.ID != state.ID {
		return NativeSession{}, fmt.Errorf("Pi resumed a different session")
	}
	return NativeSession{ID: state.ID, Path: state.File}, nil
}

func (piAdapter) send(ctx context.Context, p *peer, native NativeSession, prompt string) (sendResult, error) {
	raw, err := piCall(ctx, p, "prompt", map[string]any{"message": prompt})
	if err != nil {
		return sendResult{}, err
	}
	var result struct {
		Disposition string `json:"disposition"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return sendResult{}, err
	}
	if result.Disposition != "started" && result.Disposition != "handled" {
		return sendResult{}, fmt.Errorf("unsupported Pi prompt disposition %q", result.Disposition)
	}
	return sendResult{Handled: result.Disposition == "handled"}, nil
}

func (piAdapter) interrupt(ctx context.Context, p *peer, native NativeSession, turn *Turn) error {
	// Pi abort 会继续已有队列；先清空队列才与“中断当前任务”的控制语义一致。
	if _, err := piCall(ctx, p, "clear_queue", nil); err != nil {
		return err
	}
	_, err := piCall(ctx, p, "abort", nil)
	return err
}

func (piAdapter) observe(raw json.RawMessage) observation {
	var event struct {
		Type    string          `json:"type"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Timeout int64           `json:"timeout"`
		Aborted bool            `json:"aborted"`
		Message struct {
			Role       string `json:"role"`
			StopReason string `json:"stopReason"`
			Error      string `json:"errorMessage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return observation{Error: err.Error()}
	}
	switch event.Type {
	case "agent_start":
		return observation{Started: true}
	case "message_end":
		if event.Message.Role == "assistant" {
			err := event.Message.Error
			if event.Message.StopReason == "error" && err == "" {
				err = "Pi assistant response failed"
			}
			return observation{AssistantEnded: true, Error: err}
		}
	case "agent_settled":
		state := "completed"
		if event.Aborted {
			state = "interrupted"
		}
		return observation{Completed: true, State: state}
	case "extension_ui_request":
		switch event.Method {
		case "select", "confirm", "input", "editor":
			return observation{Input: input(event.Method, event.ID, raw, time.Duration(event.Timeout)*time.Millisecond)}
		}
	}
	return observation{}
}

func (piAdapter) respond(p *peer, request pendingInput, raw json.RawMessage) error {
	response, err := responseObject(raw)
	if err != nil {
		return err
	}
	for key := range response {
		if key != "cancelled" && key != "confirmed" && key != "value" {
			return fmt.Errorf("unsupported Pi response field %q", key)
		}
	}
	var cancelled bool
	if v, ok := response["cancelled"]; ok {
		if err := json.Unmarshal(v, &cancelled); err != nil {
			return fmt.Errorf("cancelled must be boolean")
		}
	}
	if !cancelled {
		if request.Method == "confirm" {
			var confirmed bool
			if v, ok := response["confirmed"]; !ok || string(v) == "null" || json.Unmarshal(v, &confirmed) != nil {
				return fmt.Errorf("confirm response requires boolean confirmed")
			}
		} else {
			var value string
			if v, ok := response["value"]; !ok || string(v) == "null" || json.Unmarshal(v, &value) != nil {
				return fmt.Errorf("dialog response requires string value")
			}
			if request.Method == "select" {
				var params struct {
					Options []string `json:"options"`
				}
				if err := json.Unmarshal(request.Params, &params); err != nil {
					return err
				}
				found := false
				for _, option := range params.Options {
					if option == value {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("value is not a Pi dialog option")
				}
			}
		}
	}
	response["type"] = json.RawMessage(`"extension_ui_response"`)
	response["id"] = request.NativeID
	return p.write(response)
}

func (piAdapter) cancel(pendingInput) json.RawMessage { return json.RawMessage(`{"cancelled":true}`) }
