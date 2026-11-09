// Package session 只管理外部 Agent 的协议进程与控制状态，不执行模型或工具。
package session

import (
	"encoding/json"
	"time"
)

const (
	protocolVersion = 1
	startupTimeout  = 60 * time.Second
	requestTimeout  = 30 * time.Second
	inputTimeout    = 5 * time.Minute
	maxFrame        = 16 << 20
)

type NativeSession struct {
	ID   string `json:"id"`
	Path string `json:"path,omitempty"`
}

type Turn struct {
	ID          string    `json:"id"`
	NativeID    string    `json:"nativeId,omitempty"`
	Accepted    bool      `json:"accepted"`
	State       string    `json:"state"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type RequestInfo struct {
	ID         string          `json:"id"`
	Method     string          `json:"method"`
	ExpiresAt  time.Time       `json:"expiresAt"`
	Parameters json.RawMessage `json:"params,omitempty"`
}

// Snapshot 只保存生命周期与原生会话映射，不保存提示词、对话或解析后的环境变量。
type Snapshot struct {
	SchemaVersion int           `json:"schemaVersion"`
	ID            string        `json:"id"`
	Instance      string        `json:"instance"`
	Profile       string        `json:"profile"`
	Adapter       string        `json:"adapter"`
	CWD           string        `json:"cwd"`
	State         string        `json:"state"`
	WorkerPID     int           `json:"workerPid,omitempty"`
	AgentPID      int           `json:"agentPid,omitempty"`
	Native        NativeSession `json:"native"`
	Turn          *Turn         `json:"turn,omitempty"`
	Pending       []RequestInfo `json:"pending,omitempty"`
	Sequence      uint64        `json:"sequence"`
	CreatedAt     time.Time     `json:"createdAt"`
	UpdatedAt     time.Time     `json:"updatedAt"`
	Error         string        `json:"error,omitempty"`
}

type Event struct {
	Sequence uint64          `json:"sequence"`
	Time     time.Time       `json:"time"`
	Type     string          `json:"type"`
	TurnID   string          `json:"turnId,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

type Request struct {
	Version   int             `json:"version"`
	Instance  string          `json:"instance"`
	Method    string          `json:"method"`
	Prompt    string          `json:"prompt,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
	Response  json.RawMessage `json:"response,omitempty"`
	After     uint64          `json:"after,omitempty"`
	Wait      bool            `json:"wait,omitempty"`
}

type Reply struct {
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
	Session *Snapshot `json:"session,omitempty"`
	Event   *Event    `json:"event,omitempty"`
}

func terminal(state string) bool {
	return state == "closed" || state == "failed" || state == "unreachable"
}

func turnActive(turn *Turn) bool {
	return turn != nil && (turn.State == "submitted" || turn.State == "running" || turn.State == "interrupting")
}
