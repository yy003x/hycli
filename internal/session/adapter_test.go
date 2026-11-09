package session

import (
	"encoding/json"
	"testing"
)

func TestPiCompletionRequiresSettled(t *testing.T) {
	driver := piAdapter{}
	for _, raw := range []string{`{"type":"response","command":"prompt","success":true,"data":{"disposition":"started"}}`, `{"type":"agent_end","willRetry":false}`, `{"type":"auto_retry_end","success":true}`} {
		if driver.observe(json.RawMessage(raw)).Completed {
			t.Fatalf("premature completion: %s", raw)
		}
	}
	for _, test := range []struct{ raw, state string }{{`{"type":"agent_settled","aborted":false}`, "completed"}, {`{"type":"agent_settled","aborted":true}`, "interrupted"}} {
		result := driver.observe(json.RawMessage(test.raw))
		if !result.Completed || result.State != test.state {
			t.Fatalf("%+v", result)
		}
	}
}

func TestCodexCompletionAndInputClassification(t *testing.T) {
	driver := codexAdapter{}
	if driver.observe(json.RawMessage(`{"id":"hy-1","result":{"turn":{"id":"t","status":"inProgress"}}}`)).Completed {
		t.Fatal("acknowledgement is not completion")
	}
	result := driver.observe(json.RawMessage(`{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn","status":"failed","error":{"message":"provider unavailable"}}}}`))
	if !result.Completed || result.State != "failed" || result.Error != "provider unavailable" || result.NativeTurnID != "turn" {
		t.Fatalf("%+v", result)
	}
	for _, method := range []string{"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/tool/requestUserInput", "item/permissions/requestApproval", "mcpServer/elicitation/request"} {
		raw, _ := json.Marshal(map[string]any{"id": 7, "method": method, "params": map[string]any{}})
		request := driver.observe(raw).Input
		if request == nil || request.Method != method || !json.Valid(driver.cancel(*request)) {
			t.Fatalf("request lost: %s", raw)
		}
	}
	unknown := driver.observe(json.RawMessage(`{"id":8,"method":"unknown/request","params":{}}`))
	if len(unknown.UnsupportedID) == 0 {
		t.Fatal("unknown server request must be rejected explicitly")
	}
}

func TestPiNotificationsAreNotBlockingDialogs(t *testing.T) {
	driver := piAdapter{}
	if driver.observe(json.RawMessage(`{"type":"extension_ui_request","id":"q1","method":"notify","message":"hello"}`)).Input != nil {
		t.Fatal("notify does not require a response")
	}
	request := driver.observe(json.RawMessage(`{"type":"extension_ui_request","id":"q2","method":"confirm","title":"Allow?"}`)).Input
	if request == nil || string(driver.cancel(*request)) != `{"cancelled":true}` {
		t.Fatal("unanswered Pi dialog must be cancelled")
	}
}

func TestAdapterRejectsLifecycleOverrides(t *testing.T) {
	for _, test := range []struct {
		driver adapter
		args   []string
	}{{piAdapter{}, []string{"--mode=json"}}, {piAdapter{}, []string{"--no-session"}}, {codexAdapter{}, []string{"--listen", "ws://127.0.0.1:9000"}}, {codexAdapter{}, []string{"app-server"}}} {
		if _, err := test.driver.arguments(test.args); err == nil {
			t.Fatalf("accepted lifecycle override: %v", test.args)
		}
	}
}
