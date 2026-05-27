package model

import "time"

type Run struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	StartTime     time.Time         `json:"startTime"`
	EndTime       time.Time         `json:"endTime"`
	Tags          map[string]string `json:"tags"`
	Model         string            `json:"model"`
	Vendor        string            `json:"vendor"`
	TaskID        string            `json:"taskId"`
	TaskSource    TaskSource        `json:"taskSource"`
	PromptHash    string            `json:"promptHash"`
	ReplayOf      *string           `json:"replayOf,omitempty"`
	ReplayGroup   *string           `json:"replayGroup,omitempty"`
	IngestSource  string            `json:"ingestSource"`
	Totals        Usage             `json:"totals"`
	Steps         []AgentStep       `json:"steps"`
	EvalResults   []EvalResult      `json:"evalResults,omitempty"`
	Feedback      []FeedbackEntry   `json:"feedback,omitempty"`
}

type TaskSource string

const (
	TaskSourceExplicit       TaskSource = "explicit"
	TaskSourceTaskName       TaskSource = "task_name"
	TaskSourceConversationID TaskSource = "conversation_id"
	TaskSourcePromptHash     TaskSource = "prompt_hash"
	TaskSourceManual         TaskSource = "manual"
	TaskSourceUnknown        TaskSource = "unknown"
)

type StepKind string

const (
	StepKindPlannerLLM  StepKind = "PlannerLLM"
	StepKindToolCall    StepKind = "ToolCall"
	StepKindObservation StepKind = "Observation"
	StepKindAgentInvoke StepKind = "AgentInvoke"
	StepKindOther       StepKind = "Other"
)

type AgentStep struct {
	Index         int             `json:"index"`
	SpanID        string          `json:"spanId"`
	ParentSpanID  string          `json:"parentSpanId,omitempty"`
	Kind          StepKind        `json:"kind"`
	StartTime     time.Time       `json:"startTime"`
	Duration      time.Duration   `json:"duration"`
	Model         string          `json:"model,omitempty"`
	Operation     string          `json:"operation,omitempty"`
	AgentName     string          `json:"agentName,omitempty"`
	Tool          *ToolInvocation `json:"tool,omitempty"`
	Messages      []Message       `json:"messages,omitempty"`
	Output        string          `json:"output,omitempty"`
	Usage         Usage           `json:"usage"`
	Evals         []EvalResult    `json:"evals,omitempty"`
	StatusError   string          `json:"statusError,omitempty"`
}

type ToolInvocation struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"` // raw JSON
	Result    string `json:"result,omitempty"`    // raw text or JSON
	Error     string `json:"error,omitempty"`
}

type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content,omitempty"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
}

type Usage struct {
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	TotalTokens  int64   `json:"totalTokens"`
	CostUSD      float64 `json:"costUsd,omitempty"`
}

type EvalResult struct {
	Name    string  `json:"name"`
	Score   float64 `json:"score,omitempty"`
	Label   string  `json:"label,omitempty"`
	Comment string  `json:"comment,omitempty"`
}

type FeedbackEntry struct {
	ID        string    `json:"id"`
	TraceID   string    `json:"traceId"`
	SpanID    string    `json:"spanId,omitempty"`
	Source    string    `json:"source"` // user | evaluator | ground_truth
	Score     float64   `json:"score"`
	Label     string    `json:"label,omitempty"`
	Comment   string    `json:"comment,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type Task struct {
	ID           string     `json:"id"`
	Source       TaskSource `json:"source"`
	Name         string     `json:"name,omitempty"`
	RunCount     int        `json:"runCount"`
	Models       []string   `json:"models"`
	PromptHashes []string   `json:"promptHashes,omitempty"`
	LastSeen     time.Time  `json:"lastSeen"`
	AvgScore     *float64   `json:"avgScore,omitempty"`
}

type ReplayGroup struct {
	GroupID string       `json:"groupId"`
	Members []ReplayLink `json:"members"`
}

type ReplayLink struct {
	TraceID string `json:"traceId"`
	Role    string `json:"role"` // primary | shadow
}
