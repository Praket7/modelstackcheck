package doctor

import (
	"net/http"
	"time"
)

const SchemaVersion = "1.0"

const (
	ExitOK = iota
	ExitWarning
	ExitFailure
	ExitConfiguration
	ExitInternal
)

type Options struct {
	Provider string
	Model    string
	Endpoint string
	Harness  string
	Profile  string
	Timeout  time.Duration
}

type Check struct {
	ID         string `json:"probe"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"latency_ms"`
	Evidence   string `json:"evidence,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Diagnosis struct {
	Code           string   `json:"code"`
	Confidence     string   `json:"confidence"`
	Summary        string   `json:"summary"`
	Evidence       []string `json:"evidence"`
	Recommendation string   `json:"recommendation"`
}

type Report struct {
	SchemaVersion string      `json:"schema_version"`
	GeneratedAt   time.Time   `json:"generated_at"`
	Provider      string      `json:"provider"`
	Endpoint      string      `json:"endpoint,omitempty"`
	Model         string      `json:"model,omitempty"`
	Harness       string      `json:"harness,omitempty"`
	System        string      `json:"system"`
	Architecture  string      `json:"architecture"`
	Checks        []Check     `json:"results"`
	Diagnoses     []Diagnosis `json:"diagnoses"`
	Notes         []string    `json:"notes,omitempty"`
}

func (r *Report) ExitCode() int {
	for _, check := range r.Checks {
		if check.Status == "fail" || check.Status == "error" {
			return ExitFailure
		}
	}
	for _, check := range r.Checks {
		if check.Status == "warn" {
			return ExitWarning
		}
	}
	return ExitOK
}

type apiClient struct {
	base  string
	key   string
	model string
	http  *http.Client
}

type chatRequest struct {
	Model             string     `json:"model"`
	Messages          []message  `json:"messages"`
	Tools             []toolSpec `json:"tools,omitempty"`
	ParallelToolCalls bool       `json:"parallel_tool_calls,omitempty"`
	Stream            bool       `json:"stream,omitempty"`
}

type message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolSpec struct {
	Type     string       `json:"type"`
	Function functionSpec `json:"function"`
}

type functionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type toolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function calledFunction `json:"function"`
}

type calledFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}
