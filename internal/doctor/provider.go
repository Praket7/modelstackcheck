package doctor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (c *apiClient) endpoint(path string) string { return strings.TrimRight(c.base, "/") + path }

func (c *apiClient) request(ctx context.Context, method, address string, body any) (*http.Response, error) {
	var src io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		src = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, src)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("provider request timed out")
		}
		return nil, errors.New("could not reach the configured provider")
	}
	return resp, nil
}

func (c *apiClient) models(ctx context.Context) ([]string, error) {
	resp, err := c.request(ctx, http.MethodGet, c.endpoint("/models"), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(resp)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(body.Data))
	for _, item := range body.Data {
		if item.ID == "" {
			continue
		}
		models = append(models, item.ID)
	}
	if len(models) == 0 {
		return nil, errors.New("provider returned no models")
	}
	return models, nil
}

func (c *apiClient) chat(ctx context.Context, messages []message, tools []toolSpec, parallel bool) (*chatResponse, error) {
	reqBody := chatRequest{Model: c.model, Messages: messages, Tools: tools, ParallelToolCalls: parallel}
	resp, err := c.request(ctx, http.MethodPost, c.endpoint("/chat/completions"), reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(resp)
	}
	var body chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, err
	}
	if len(body.Choices) == 0 {
		return nil, errors.New("provider returned no response choices")
	}
	return &body, nil
}

func (c *apiClient) stream(ctx context.Context) error {
	resp, err := c.request(ctx, http.MethodPost, c.endpoint("/chat/completions"), chatRequest{
		Model: c.model, Stream: true, Messages: []message{{Role: "user", Content: "Reply with the word READY"}},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(resp)
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return errors.New("provider did not return an event stream")
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 4<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	seen := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			seen = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !seen {
		return errors.New("event stream contained no text")
	}
	return nil
}

func responseError(resp *http.Response) error {
	return fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
}

func makeTools(names ...string) []toolSpec {
	defs := map[string]functionSpec{
		"read_file":       {Name: "read_file", Description: "Read a file by its path", Parameters: objectSchema(map[string]any{"path": map[string]any{"type": "string"}}, "path")},
		"search":          {Name: "search", Description: "Search text in project files", Parameters: objectSchema(map[string]any{"query": map[string]any{"type": "string"}}, "query")},
		"inspect_profile": {Name: "inspect_profile", Description: "Inspect a profile and its settings", Parameters: objectSchema(map[string]any{"profile": objectSchema(map[string]any{"name": map[string]any{"type": "string"}, "settings": objectSchema(map[string]any{"theme": map[string]any{"type": "string"}}, "theme")}, "name", "settings")}, "profile")},
		"list_files":      {Name: "list_files", Description: "List available project files", Parameters: objectSchema(map[string]any{"path": map[string]any{"type": "string"}}, "path")},
		"set_mode":        {Name: "set_mode", Description: "Choose a supported operation mode", Parameters: objectSchema(map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"read", "write", "append"}}}, "mode")},
		"edit_file":       {Name: "edit_file", Description: "Replace exact text in a fixture file", Parameters: objectSchema(map[string]any{"path": map[string]any{"type": "string"}, "old": map[string]any{"type": "string"}, "new": map[string]any{"type": "string"}}, "path", "old", "new")},
		"run_command":     {Name: "run_command", Description: "List fixture files using the one permitted command", Parameters: objectSchema(map[string]any{"command": map[string]any{"type": "string", "enum": []string{"ls"}}}, "command")},
	}
	out := make([]toolSpec, 0, len(names))
	for _, name := range names {
		out = append(out, toolSpec{Type: "function", Function: defs[name]})
	}
	return out
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func callIsValid(call toolCall) bool {
	var args map[string]any
	return call.Function.Name != "" && json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args != nil
}

func safeEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "configured endpoint"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	host := strings.Trim(u.Hostname(), "[]")
	if host != "" && host != "localhost" && !net.ParseIP(host).IsLoopback() {
		u.Host = "<provider>"
	}
	return u.String()
}

func streamDuration(client *apiClient, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := client.stream(ctx)
	return time.Since(start), err
}
