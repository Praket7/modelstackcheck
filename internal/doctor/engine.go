package doctor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
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
	if options.Profile == "" {
		options.Profile = "quick"
	}
	if options.Profile != "quick" && options.Profile != "context" && options.Profile != "vision" && options.Profile != "full" {
		return nil, errors.New("profile must be quick, context, vision, or full")
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
		if strings.TrimSpace(textContent(second.Choices[0].Message.Content)) == "" && len(second.Choices[0].Message.ToolCalls) == 0 {
			return "", errors.New("model returned no response after the tool error")
		}
		return "Responded after receiving a simulated missing file error.", nil
	})
	if options.Profile == "context" || options.Profile == "full" {
		for _, size := range []int{8 << 10, 32 << 10} {
			size := size
			runProbe(fmt.Sprintf("context.%dk", size/1024), fmt.Sprintf("Context retrieval at about %dK tokens", size/1024), func(c context.Context) (string, error) {
				const canary = "MDOC_CONTEXT_CANARY_7F3A"
				filler := strings.Repeat("oak river cloud meadow ", size*4/22)
				prompt := fmt.Sprintf("Read this note and return only the code after CODE.\n%s\nCODE %s", filler, canary)
				resp, err := client.chat(c, []message{{Role: "user", Content: prompt}}, nil, false)
				if err != nil {
					return "", err
				}
				if !strings.Contains(textContent(resp.Choices[0].Message.Content), canary) {
					return "", errors.New("model did not retrieve the code placed at the end of the context")
				}
				return fmt.Sprintf("Retrieved the final marker from about %d tokens of generated context, estimated at four characters per token.", size/1024), nil
			})
		}
	}
	if options.Profile == "vision" || options.Profile == "full" {
		runProbe("vision.image_input", "Image understanding", func(c context.Context) (string, error) {
			content := []map[string]any{{"type": "text", "text": "What color is the central pixel in this image? Answer with one color."}, {"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + syntheticRedPNG()}}}
			resp, err := client.chat(c, []message{{Role: "user", Content: content}}, nil, false)
			if err != nil {
				return "", err
			}
			answer := strings.ToLower(textContent(resp.Choices[0].Message.Content))
			if !strings.Contains(answer, "red") {
				return "", errors.New("model did not identify the red image marker")
			}
			return "Identified the red pixel in a synthetic one pixel image.", nil
		})
	}
	if options.Harness == "opencode" {
		startedHarness := time.Now()
		evidence, err := runOpenCode(ctx, client, model, options.Timeout)
		check := Check{ID: "harness.opencode", Name: "OpenCode execution", Status: "pass", DurationMS: time.Since(startedHarness).Milliseconds(), Evidence: evidence}
		if err != nil {
			check.Status = "fail"
			check.Error = Redact(err.Error())
			check.Evidence = "The controlled fixture was not completed through OpenCode."
		}
		report.Checks = append(report.Checks, check)
	}
	report.Diagnoses = diagnose(report.Checks)
	if report.Harness != "" {
		report.Notes = append(report.Notes, "The OpenCode check uses an isolated temporary project and configuration, and grants read access only to its fixture.")
	}
	if report.Harness == "" && options.Harness == "opencode" {
		report.Notes = append(report.Notes, "OpenCode was requested but was not detected.")
	}
	if len(report.Diagnoses) == 0 {
		evidence := "Provider connection, streaming, tool calls, and recovery passed."
		recommendation := "No issue appeared in the selected checks, so try the setup with the task that first failed."
		for _, check := range report.Checks {
			if check.ID == "harness.opencode" && check.Status == "pass" {
				evidence += " OpenCode also completed the read only fixture."
			}
		}
		report.Diagnoses = append(report.Diagnoses, Diagnosis{Code: "NO_FAILURE_OBSERVED", Confidence: "medium", Summary: "The tested checks passed in this run.", Evidence: []string{evidence}, Recommendation: recommendation})
	}
	return report, nil
}

func selectProvider(options Options) (*apiClient, string, string, func(), error) {
	name := strings.ToLower(options.Provider)
	base, key, model := options.Endpoint, os.Getenv("OPENAI_API_KEY"), options.Model
	if name == "mock" {
		server := httptest.NewServer(mockHandler())
		if model == "" {
			model = "modelstackcheck-mock"
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
	if failed["harness.opencode"] {
		out = append(out, Diagnosis{Code: "HARNESS_EXECUTION", Confidence: "high", Summary: "The controlled tool call did not complete through OpenCode.", Evidence: []string{"The check used a temporary project and a provider fixture."}, Recommendation: "Compare the OpenCode model and provider settings with the direct provider results, then repeat the harness check."})
	}
	if failed["context.8k"] || failed["context.32k"] {
		out = append(out, Diagnosis{Code: "CONTEXT_RETRIEVAL", Confidence: "medium", Summary: "The model did not retrieve a marker placed at the end of a long prompt.", Evidence: []string{"The context probe failed at one or more requested sizes."}, Recommendation: "Lower the prompt size or raise the provider context limit, then repeat the same retrieval check."})
	}
	if failed["vision.image_input"] {
		out = append(out, Diagnosis{Code: "PROVIDER_VISION", Confidence: "medium", Summary: "The model did not identify the image sent with the request.", Evidence: []string{"The synthetic image recognition probe failed."}, Recommendation: "Choose a vision enabled model and confirm the provider supports image input."})
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
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "modelstackcheck-mock", "object": "model"}}})
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
			writeMockStream(w, mockAnswer(request))
			return
		}
		answer := mockAnswer(request)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": answer}}})
	})
	return mux
}

func writeMockStream(w http.ResponseWriter, answer message) {
	var delta map[string]any
	finishReason := "stop"
	if len(answer.ToolCalls) > 0 {
		finishReason = "tool_calls"
		call := answer.ToolCalls[0]
		delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": call.ID, "type": "function", "function": map[string]string{"name": call.Function.Name, "arguments": call.Function.Arguments}}}}
	} else {
		content := textContent(answer.Content)
		if content == "" {
			content = "READY"
		}
		delta = map[string]any{"content": content}
	}
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finishReason}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
}

func mockAnswer(req chatRequest) message {
	text := ""
	for _, msg := range req.Messages {
		text += " " + strings.ToLower(textContent(msg.Content))
	}
	for _, msg := range req.Messages {
		if msg.Role == "tool" && strings.Contains(textContent(msg.Content), "MDOC_") {
			return message{Role: "assistant", Content: "I read the fixture. Its exact code is " + strings.TrimSpace(textContent(msg.Content))}
		}
	}
	if strings.Contains(text, "mdoc_context_canary_7f3a") {
		return message{Role: "assistant", Content: "MDOC_CONTEXT_CANARY_7F3A"}
	}
	encoded, _ := json.Marshal(req.Messages)
	if strings.Contains(string(encoded), "data:image/png") {
		return message{Role: "assistant", Content: "red"}
	}
	if strings.Contains(text, "mdoc_opencode_canary") {
		for _, candidate := range req.Tools {
			if candidate.Function.Name != "read" && candidate.Function.Name != "read_file" {
				continue
			}
			args := map[string]any{}
			properties, _ := candidate.Function.Parameters["properties"].(map[string]any)
			for key := range properties {
				if key == "filePath" || key == "path" {
					args[key] = "fixture.txt"
				}
			}
			data, _ := json.Marshal(args)
			return message{Role: "assistant", ToolCalls: []toolCall{{ID: "opencode_fixture_read", Type: "function", Function: calledFunction{Name: candidate.Function.Name, Arguments: string(data)}}}}
		}
		return message{Role: "assistant", Content: "I cannot find a read tool."}
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

func textContent(value any) string {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		var out strings.Builder
		for _, part := range content {
			if item, ok := part.(map[string]any); ok {
				if s, ok := item["text"].(string); ok {
					out.WriteString(s)
				}
			}
		}
		return out.String()
	default:
		return ""
	}
}

func syntheticRedPNG() string {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var data bytes.Buffer
	_ = png.Encode(&data, img)
	return base64.StdEncoding.EncodeToString(data.Bytes())
}

func runOpenCode(ctx context.Context, client *apiClient, model string, timeout time.Duration) (string, error) {
	path, err := exec.LookPath("opencode")
	if err != nil {
		return "", errors.New("OpenCode CLI was not found on PATH")
	}
	root, err := os.MkdirTemp("", "mdoc-opencode-")
	if err != nil {
		return "", errors.New("could not create the isolated OpenCode fixture")
	}
	defer os.RemoveAll(root)
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		return "", errors.New("could not create the isolated OpenCode project")
	}
	const canary = "MDOC_OPENCODE_CANARY_91C4"
	if err := os.WriteFile(filepath.Join(project, "fixture.txt"), []byte(canary+"\n"), 0600); err != nil {
		return "", errors.New("could not create the isolated OpenCode fixture")
	}
	providerModel := model
	if providerModel == "" {
		providerModel = "probe"
		client.model = providerModel
	}
	configuration := map[string]any{
		"$schema":    "https://opencode.ai/config.json",
		"model":      "modelstackcheck/" + providerModel,
		"permission": map[string]string{"edit": "deny", "bash": "deny", "external_directory": "deny"},
		"provider": map[string]any{"modelstackcheck": map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": "ModelStackCheck isolated provider",
			"options": map[string]any{"baseURL": client.base, "apiKey": client.key},
			"models":  map[string]any{providerModel: map[string]any{"name": "Isolated probe model", "tool_call": true, "limit": map[string]int{"context": 65536, "output": 4096}}},
		}},
	}
	data, _ := json.Marshal(configuration)
	configPath := filepath.Join(root, "opencode.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return "", errors.New("could not create the isolated OpenCode configuration")
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, path, "run", "--pure", "--agent", "plan", "--model", "modelstackcheck/"+providerModel, "--format", "json", "--dir", project, "Read fixture.txt and return the exact marker that it contains. Include MDOC_OPENCODE_CANARY in your tool call plan.")
	cmd.Dir = project
	cmd.Env = isolatedEnv(os.Environ(), map[string]string{"OPENCODE_CONFIG": configPath, "OPENCODE_CONFIG_DIR": root, "OPENCODE_DISABLE_MODELS_FETCH": "1", "OPENCODE_DISABLE_DEFAULT_PLUGINS": "1", "HOME": root, "USERPROFILE": root, "XDG_CONFIG_HOME": root, "XDG_DATA_HOME": root, "APPDATA": root})
	output, err := cmd.CombinedOutput()
	if err != nil {
		if probeCtx.Err() != nil {
			return "", errors.New("OpenCode did not finish the read only fixture before the timeout")
		}
		return "", errors.New("OpenCode could not complete the read only fixture")
	}
	if !strings.Contains(string(output), canary) {
		return "", errors.New("OpenCode did not return the fixture marker")
	}
	return "OpenCode read the temporary fixture through the selected provider and returned its marker.", nil
}

func isolatedEnv(environment []string, replacements map[string]string) []string {
	out := make([]string, 0, len(environment)+len(replacements))
	for _, item := range environment {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, replace := replacements[key]; !replace {
			out = append(out, item)
		}
	}
	for key, value := range replacements {
		out = append(out, key+"="+value)
	}
	return out
}
