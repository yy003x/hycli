package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yy003x/hycli/internal/buildinfo"
)

type codexAdapter struct{}

func (codexAdapter) arguments(args []string) ([]string, error) {
	if err := rejectOwnedArgs(args, "app-server", "--listen", "--stdio", "--"); err != nil {
		return nil, err
	}
	return append(append([]string{}, args...), "app-server", "--listen", "stdio://"), nil
}

func codexCall(ctx context.Context, p *peer, method string, params any) (json.RawMessage, error) {
	return p.call(ctx, map[string]any{"method": method, "params": params})
}

func (codexAdapter) initialize(ctx context.Context, p *peer, cwd string, previous NativeSession) (NativeSession, error) {
	_, err := codexCall(ctx, p, "initialize", map[string]any{"clientInfo": map[string]string{"name": "hycli", "version": buildinfo.Version}})
	if err != nil {
		return NativeSession{}, err
	}
	if err := p.write(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return NativeSession{}, err
	}
	method := "thread/start"
	// 权限与模型沿用 Codex 自身配置，不覆盖为自动批准或绕过沙箱。
	params := map[string]any{"cwd": cwd}
	if previous.ID != "" {
		method = "thread/resume"
		params["threadId"] = previous.ID
		params["excludeTurns"] = true
	}
	raw, err := codexCall(ctx, p, method, params)
	if err != nil {
		return NativeSession{}, err
	}
	var result struct {
		Thread struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return NativeSession{}, err
	}
	if result.Thread.ID == "" {
		return NativeSession{}, fmt.Errorf("Codex returned no thread ID")
	}
	if previous.ID != "" && previous.ID != result.Thread.ID {
		return NativeSession{}, fmt.Errorf("Codex resumed a different thread")
	}
	return NativeSession{ID: result.Thread.ID, Path: result.Thread.Path}, nil
}

func (codexAdapter) send(ctx context.Context, p *peer, native NativeSession, prompt string) (sendResult, error) {
	raw, err := codexCall(ctx, p, "turn/start", map[string]any{"threadId": native.ID, "input": []any{map[string]any{"type": "text", "text": prompt}}})
	if err != nil {
		return sendResult{}, err
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return sendResult{}, err
	}
	if result.Turn.ID == "" {
		return sendResult{}, fmt.Errorf("Codex returned no turn ID")
	}
	return sendResult{NativeID: result.Turn.ID}, nil
}

func (codexAdapter) interrupt(ctx context.Context, p *peer, native NativeSession, turn *Turn) error {
	if turn == nil || turn.NativeID == "" {
		return fmt.Errorf("Codex turn is not yet ready to interrupt; retry after its acknowledgement")
	}
	_, err := codexCall(ctx, p, "turn/interrupt", map[string]any{"threadId": native.ID, "turnId": turn.NativeID})
	return err
}

func (codexAdapter) observe(raw json.RawMessage) observation {
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return observation{Error: err.Error()}
	}
	if strings.HasPrefix(message.Method, "codex/event/") {
		return observation{Skip: true}
	}
	if len(message.ID) > 0 && string(message.ID) != "null" {
		switch message.Method {
		case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request":
			return observation{Input: input(message.Method, message.ID, message.Params, inputTimeout)}
		default:
			return observation{UnsupportedID: message.ID}
		}
	}
	var params struct {
		ThreadID  string          `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
		Turn      struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return observation{}
	}
	result := observation{ThreadID: params.ThreadID, NativeTurnID: params.Turn.ID}
	switch message.Method {
	case "turn/started":
		result.Started = true
	case "turn/completed":
		result.Completed, result.State = true, params.Turn.Status
		if params.Turn.Error != nil {
			result.Error = params.Turn.Error.Message
		}
		if result.State != "completed" && result.State != "failed" && result.State != "interrupted" {
			result.State, result.Error = "failed", "unknown Codex terminal status"
		}
	case "serverRequest/resolved":
		result.ResolvedID = params.RequestID
	}
	return result
}

func (codexAdapter) respond(p *peer, request pendingInput, raw json.RawMessage) error {
	response, err := responseObject(raw)
	if err != nil {
		return err
	}
	switch request.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		var decision string
		if json.Unmarshal(response["decision"], &decision) != nil {
			return fmt.Errorf("response requires a string decision")
		}
		if decision != "accept" && decision != "acceptForSession" && decision != "decline" && decision != "cancel" {
			return fmt.Errorf("unsupported approval decision %q", decision)
		}
	case "item/permissions/requestApproval":
		if _, err := responseObject(response["permissions"]); err != nil {
			return fmt.Errorf("response requires permissions object")
		}
		if value, ok := response["scope"]; ok {
			var scope string
			if json.Unmarshal(value, &scope) != nil || (scope != "turn" && scope != "session") {
				return fmt.Errorf("scope must be turn or session")
			}
		}
	case "item/tool/requestUserInput":
		var answers map[string]struct {
			Answers []string `json:"answers"`
		}
		if json.Unmarshal(response["answers"], &answers) != nil || answers == nil {
			return fmt.Errorf("response requires answers object")
		}
	case "mcpServer/elicitation/request":
		var action string
		if json.Unmarshal(response["action"], &action) != nil || (action != "accept" && action != "decline" && action != "cancel") {
			return fmt.Errorf("elicitation action must be accept, decline or cancel")
		}
	default:
		return fmt.Errorf("unsupported Codex request %q", request.Method)
	}
	return p.write(map[string]any{"id": request.NativeID, "result": json.RawMessage(raw)})
}

func (codexAdapter) cancel(request pendingInput) json.RawMessage {
	switch request.Method {
	case "item/permissions/requestApproval":
		return json.RawMessage(`{"permissions":{},"scope":"turn"}`)
	case "item/tool/requestUserInput":
		return json.RawMessage(`{"answers":{}}`)
	case "mcpServer/elicitation/request":
		return json.RawMessage(`{"action":"cancel","content":null}`)
	default:
		return json.RawMessage(`{"decision":"cancel"}`)
	}
}
