package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func Run(ctx context.Context, options Options) (*Report, error) {
	if options.Provider == "" {
		options.Provider = "auto"
	}
	if options.Harness == "" {
		options.Harness = "auto"
	}
	if options.Harness != "auto" && options.Harness != "opencode" {
		return nil, errors.New("harness must be auto or opencode")
	}
	if options.Provider != "auto" && options.Provider != "ollama" && options.Provider != "openai" && options.Provider != "mock" {
		return nil, errors.New("provider must be auto, ollama, openai, or mock")
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	client, provider, model, cleanup, err := selectProvider(options)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	client.http = &http.Client{Timeout: options.Timeout}
	started := time.Now()
	report := &Report{SchemaVersion: SchemaVersion, GeneratedAt: time.Now().UTC(), Provider: provider, Endpoint: safeEndpoint(client.base), Model: model, System: runtime.GOOS, Architecture: runtime.GOARCH}
	if provider != "mock" {
		report.Notes = append(report.Notes, "Requests were sent to the configured provider. Prompt contents are not saved in the report.")
	}
	if options.Harness == "auto" || options.Harness == "opencode" {
		report.Harness = detectOpenCode()
	}
	models, err := client.models(ctx)
	if err != nil {
		status := "fail"
		code, recommendation := "PROVIDER_CONNECTION", "Check that the provider is running and that its address is correct."
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") {
			code = "PROVIDER_AUTH"
			recommendation = "Check the API key for this provider."
		}
		if strings.Contains(err.Error(), "429") {
			code = "PROVIDER_RATE_LIMIT"
			recommendation = "Wait for the provider limit to reset, then try again."
		}
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timed out") {
			code = "RUNTIME_TIMEOUT"
			recommendation = "Increase the request timeout or use a faster model runtime."
		}
		report.Checks = append(report.Checks, Check{ID: "provider.connectivity", Name: "Provider connection", Status: status, DurationMS: time.Since(started).Milliseconds(), Error: Redact(err.Error())})
		report.Diagnoses = append(report.Diagnoses, Diagnosis{Code: code, Confidence: "high", Summary: "The provider did not return its model list.", Evidence: []string{"The model discovery request failed."}, Recommendation: recommendation})
		return report, nil
	}
	if options.Model == "" && model == "" {
		model = models[0]
		client.model = model
	}
	if options.Model != "" {
		model = options.Model
		client.model = model
	}
	report.Model = model
	report.Checks = append(report.Checks, Check{ID: "provider.connectivity", Name: "Provider connection", Status: "pass", DurationMS: time.Since(started).Milliseconds(), Evidence: fmt.Sprintf("Provider returned %d model(s).", len(models))})
	if !contains(models, model) && provider != "mock" {
		report.Notes = append(report.Notes, "The selected model name was not present in the provider model list. The request may still work if the provider accepts aliases.")
	}
	runProbe := func(id, name string, probe func(context.Context) (string, error)) {
		probeCtx, cancel := context.WithTimeout(ctx, options.Timeout)
		defer cancel()
		start := time.Now()
		evidence, err := probe(probeCtx)
		check := Check{ID: id, Name: name, Status: "pass", DurationMS: time.Since(start).Milliseconds(), Evidence: evidence}
		if err != nil {
			check.Status = "fail"
			check.Error = Redact(err.Error())
			check.Evidence = "The expected response was not observed."
		}
		report.Checks = append(report.Checks, check)
	}
	runProbe("provider.streaming", "Streaming", func(c context.Context) (string, error) {
		if err := client.stream(c); err != nil {
			return "", err
		}
		return "Received a text event before the stream ended.", nil
	})
	runProbe("tool.basic", "Basic tool call", func(c context.Context) (string, error) {
		resp, err := client.chat(c, []message{{Role: "user", Content: "Call read_file with path README.md."}}, makeTools("read_file"), false)
		if err != nil {
			return "", err
		}
		calls := resp.Choices[0].Message.ToolCalls
		if len(calls) != 1 || calls[0].Function.Name != "read_file" || !callIsValid(calls[0]) {
			return "", errors.New("model did not return the requested valid read_file call")
		}
		var args map[string]any
		_ = json.Unmarshal([]byte(calls[0].Function.Arguments), &args)
		if args["path"] != "README.md" {
			return "", errors.New("model returned the wrong file path")
		}
		return "Selected read_file and returned valid JSON arguments.", nil
	})
	runProbe("tool.nested", "Nested tool arguments", func(c context.Context) (string, error) {
		resp, err := client.chat(c, []message{{Role: "user", Content: "Call inspect_profile for profile name Ada and theme dark. Include the nested profile and settings objects."}}, makeTools("inspect_profile"), false)
		if err != nil {
			return "", err
		}
		calls := resp.Choices[0].Message.ToolCalls
		if len(calls) != 1 || calls[0].Function.Name != "inspect_profile" || !callIsValid(calls[0]) {
			return "", errors.New("model did not return the requested nested tool call")
		}
		var args map[string]any
		_ = json.Unmarshal([]byte(calls[0].Function.Arguments), &args)
		profile, _ := args["profile"].(map[string]any)
		settings, _ := profile["settings"].(map[string]any)
		if profile["name"] != "Ada" || settings["theme"] != "dark" {
			return "", errors.New("model returned incorrect nested profile values")
		}
		return "Selected inspect_profile and returned parseable nested arguments.", nil
	})
	runProbe("tool.multiple", "Multiple tool calls", func(c context.Context) (string, error) {
		resp, err := client.chat(c, []message{{Role: "user", Content: "Read README.md with read_file and search for the word doctor with search. Call both tools."}}, makeTools("read_file", "search"), true)
		if err != nil {
			return "", err
		}
		seen := map[string]bool{}
		for _, call := range resp.Choices[0].Message.ToolCalls {
			if callIsValid(call) {
				seen[call.Function.Name] = true
			}
		}
		if !seen["read_file"] || !seen["search"] {
			return "", errors.New("model did not return both requested tool calls")
		}
		return "Returned valid calls for read_file and search.", nil
	})
	runProbe("tool.failure_recovery", "Tool error recovery", func(c context.Context) (string, error) {
		first, err := client.chat(c, []message{{Role: "user", Content: "Try reading missing.txt with read_file."}}, makeTools("read_file", "list_files"), false)
		if err != nil {
			return "", err
		}
		calls := first.Choices[0].Message.ToolCalls
		if len(calls) != 1 || calls[0].Function.Name != "read_file" {
			return "", errors.New("model did not start with the expected file read")
		}
		turn := []message{{Role: "user", Content: "Try reading missing.txt with read_file."}, {Role: "assistant", ToolCalls: calls}, {Role: "tool", ToolCallID: calls[0].ID, Content: "error: file does not exist"}}
		second, err := client.chat(c, turn, makeTools("read_file", "list_files"), false)
		if err != nil {
			return "", err
		}
		if len(second.Choices[0].Message.ToolCalls) > 0 && second.Choices[0].Message.ToolCalls[0].Function.Name == "read_file" {
			return "", errors.New("model repeated the same failed tool call")
		}
		if strings.TrimSpace(second.Choices[0].Message.Content) == "" && len(second.Choices[0].Message.ToolCalls) == 0 {
			return "", errors.New("model returned no response after the tool error")
		}
		return "Responded after receiving a simulated missing file error.", nil
	})
	report.Diagnoses = diagnose(report.Checks)
	if report.Harness != "" {
		report.Notes = append(report.Notes, "OpenCode was found on this computer. This release records its presence but does not send prompts through it.")
	}
	if report.Harness == "" && options.Harness == "opencode" {
		report.Notes = append(report.Notes, "OpenCode was requested but was not detected.")
	}
	if len(report.Diagnoses) == 0 {
		report.Diagnoses = append(report.Diagnoses, Diagnosis{Code: "NO_FAILURE_OBSERVED", Confidence: "medium", Summary: "The tested provider checks passed in this run.", Evidence: []string{"Provider connection, streaming, basic tools, nested arguments, multiple tools, and recovery passed."}, Recommendation: "Try the same setup in your coding harness if the problem only occurs there."})
	}
	return report, nil
}

func selectProvider(options Options) (*apiClient, string, string, func(), error) {
	name := strings.ToLower(options.Provider)
	base, key, model := options.Endpoint, os.Getenv("OPENAI_API_KEY"), options.Model
	if name == "mock" {
		server := httptest.NewServer(mockHandler())
		if model == "" {
			model = "modeldoctor-mock"
		}
		return &apiClient{base: server.URL + "/v1", model: model}, "mock", model, server.Close, nil
	}
	if name == "auto" {
		if base == "" {
			base = os.Getenv("OPENAI_BASE_URL")
		}
		if base != "" {
			name = "openai"
			if model == "" {
				model = os.Getenv("OPENAI_MODEL")
			}
		} else {
			base = "http://127.0.0.1:11434/v1"
			if model == "" {
				model = ""
			}
			probe := &http.Client{Timeout: 1200 * time.Millisecond}
			req, _ := http.NewRequest(http.MethodGet, base+"/models", nil)
			resp, err := probe.Do(req)
			if err == nil {
				resp.Body.Close()
				name = "ollama"
			} else {
				return nil, "", "", nil, errors.New("no provider detected. Start Ollama or set OPENAI_BASE_URL and OPENAI_API_KEY")
			}
		}
	}
	if name != "ollama" && name != "openai" {
		return nil, "", "", nil, fmt.Errorf("unsupported provider %q. Choose auto, ollama, openai, or mock", options.Provider)
	}
	if name == "openai" && base == "" {
		base = os.Getenv("OPENAI_BASE_URL")
	}
	if name == "openai" && base == "" {
		return nil, "", "", nil, errors.New("set --endpoint or OPENAI_BASE_URL for an OpenAI compatible provider")
	}
	if base == "" {
		base = "http://127.0.0.1:11434/v1"
	}
	if err := validateEndpoint(base); err != nil {
		return nil, "", "", nil, err
	}
	if name == "ollama" {
		key = ""
	}
	if model == "" {
		model = os.Getenv("OPENAI_MODEL")
	}
	return &apiClient{base: strings.TrimRight(base, "/"), key: key, model: model}, name, model, nil, nil
}

func validateEndpoint(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("provider endpoint must be a valid HTTP or HTTPS URL")
	}
	return nil
}

func diagnose(checks []Check) []Diagnosis {
	failed := map[string]bool{}
	for _, check := range checks {
		failed[check.ID] = check.Status == "fail" || check.Status == "error"
	}
	var out []Diagnosis
	if failed["tool.basic"] {
		out = append(out, Diagnosis{Code: "MODEL_SCHEMA_FAILURE", Confidence: "medium", Summary: "The model did not return a valid basic tool call.", Evidence: []string{"The provider connection succeeded but the basic tool probe failed."}, Recommendation: "Try a model with tool calling support and confirm the provider exposes tools for this model."})
	}
	if failed["provider.streaming"] {
		out = append(out, Diagnosis{Code: "PROVIDER_STREAMING", Confidence: "medium", Summary: "The provider stream did not deliver a readable text response.", Evidence: []string{"The streaming probe failed."}, Recommendation: "Check streaming support in the provider and its API compatibility settings."})
	}
	if failed["tool.nested"] {
		out = append(out, Diagnosis{Code: "MODEL_SCHEMA_FAILURE", Confidence: "medium", Summary: "The model struggled with nested tool arguments.", Evidence: []string{"The nested tool probe failed."}, Recommendation: "Simplify nested tool schemas or select a model that handles structured arguments reliably."})
	}
	if failed["tool.multiple"] {
		out = append(out, Diagnosis{Code: "CONFIG_TOOL_PARALLELISM", Confidence: "medium", Summary: "The model did not return both requested tool calls.", Evidence: []string{"The multiple tool probe failed."}, Recommendation: "Disable parallel tool calls in the coding harness and repeat the probe."})
	}
	if failed["tool.failure_recovery"] {
		out = append(out, Diagnosis{Code: "MODEL_TOOL_SELECTION", Confidence: "medium", Summary: "The model did not recover cleanly after a tool error.", Evidence: []string{"The simulated missing file error was not handled as expected."}, Recommendation: "Use clearer tool error messages or a model with stronger tool recovery behavior."})
	}
	return out
}

func detectOpenCode() string {
	if _, err := os.Stat("opencode.json"); err == nil {
		return "OpenCode"
	}
	if _, err := os.Stat(".opencode"); err == nil {
		return "OpenCode"
	}
	if _, err := exec.LookPath("opencode"); err == nil {
		return "OpenCode"
	}
	if config, err := os.UserConfigDir(); err == nil {
		for _, path := range []string{filepath.Join(config, "opencode", "opencode.json"), filepath.Join(config, "opencode", "opencode.jsonc")} {
			if _, err := os.Stat(path); err == nil {
				return "OpenCode"
			}
		}
	}
	for _, path := range []string{"/Applications/OpenCode.app", "/Applications/OpenCode.app/Contents/MacOS/OpenCode"} {
		if _, err := os.Stat(path); err == nil {
			return "OpenCode"
		}
	}
	return ""
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func ServeMock(address string) error {
	server := &http.Server{Addr: address, Handler: mockHandler(), ReadHeaderTimeout: 5 * time.Second}
	fmt.Printf("Mock provider listening at http://%s/v1\n", address)
	return server.ListenAndServe()
}

func mockHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "modeldoctor-mock", "object": "model"}}})
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"READY\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		answer := mockAnswer(request)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": answer}}})
	})
	return mux
}

func mockAnswer(req chatRequest) message {
	text := ""
	for _, msg := range req.Messages {
		text += " " + strings.ToLower(msg.Content)
	}
	for _, msg := range req.Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "MDOC_CHECK_") {
			return message{Role: "assistant", Content: "I read the fixture. Its exact code is " + strings.TrimSpace(msg.Content)}
		}
	}
	if strings.Contains(text, "does not exist") {
		return message{Role: "assistant", Content: "The file is missing. I can list available files."}
	}
	var calls []toolCall
	add := func(name, args string) {
		calls = append(calls, toolCall{ID: fmt.Sprintf("call_%d", len(calls)+1), Type: "function", Function: calledFunction{Name: name, Arguments: args}})
	}
	switch {
	case strings.Contains(text, "inspect_profile") || strings.Contains(text, "profile name ada"):
		add("inspect_profile", `{"profile":{"name":"Ada","settings":{"theme":"dark"}}}`)
	case strings.Contains(text, "both tools") || strings.Contains(text, "readme.md and search"):
		add("read_file", `{"path":"README.md"}`)
		add("search", `{"query":"doctor"}`)
	case strings.Contains(text, "missing.txt"):
		add("read_file", `{"path":"missing.txt"}`)
	case strings.Contains(text, "readme.md"):
		add("read_file", `{"path":"README.md"}`)
	case strings.Contains(text, "fixture.txt"):
		for _, spec := range req.Tools {
			name := spec.Function.Name
			if name != "read" && name != "read_file" {
				continue
			}
			properties, _ := spec.Function.Parameters["properties"].(map[string]any)
			arg := "filePath"
			if _, ok := properties[arg]; !ok {
				arg = "path"
			}
			value, _ := json.Marshal(map[string]string{arg: "fixture.txt"})
			add(name, string(value))
			break
		}
	default:
		for _, tool := range req.Tools {
			if tool.Function.Name == "read_file" {
				add("read_file", `{"path":"README.md"}`)
				break
			}
		}
	}
	return message{Role: "assistant", ToolCalls: calls, Content: "All requested checks completed."}
}
