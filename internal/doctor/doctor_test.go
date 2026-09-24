package doctor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMockFullProfilePassesContextAndVision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := Run(ctx, Options{Provider: "mock", Profile: "full", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"context.8k", "context.32k", "vision.image_input"} {
		found := false
		for _, check := range report.Checks {
			if check.ID == id && check.Status == "pass" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing passing check %q in %#v", id, report.Checks)
		}
	}
}

func TestSyntheticVisionImageIsRed(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(syntheticRedPNG())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(4, 4).RGBA()
	if r < 0xffff || g != 0 || b != 0 {
		t.Fatalf("unexpected pixel rgb %x %x %x", r, g, b)
	}
}

func TestFixProviderTimeoutPreviewApplyAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	original := []byte(`{"model":"x/y"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := FixProviderTimeout(path, "x", 10*time.Minute, false)
	if err != nil || !strings.Contains(preview, "Preview only") {
		t.Fatalf("preview %q, %v", preview, err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(original) {
		t.Fatal("preview changed the config")
	}
	if _, err := FixProviderTimeout(path, "x", 10*time.Minute, true); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil || string(backup) != string(original) {
		t.Fatalf("backup did not preserve original config: %s %v", backup, err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(updated, &config); err != nil {
		t.Fatal(err)
	}
	provider := config["provider"].(map[string]any)["x"].(map[string]any)
	if provider["options"].(map[string]any)["timeout"].(float64) != float64((10 * time.Minute).Milliseconds()) {
		t.Fatal("provider timeout was not updated")
	}
	if _, err := FixProviderTimeout(path, "x", time.Hour, true); err == nil {
		t.Fatal("overwrote an existing backup")
	}
}

func TestMockProviderDoctorPasses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	report, err := Run(ctx, Options{Provider: "mock", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if report.Provider != "mock" || report.Model != "modelstackcheck-mock" {
		t.Fatalf("unexpected provider metadata: %+v", report)
	}
	if report.ExitCode() != ExitOK {
		t.Fatalf("mock diagnostics were not green: %s", Text(report))
	}
	if len(report.Checks) != 6 {
		t.Fatalf("got %d checks, expected six", len(report.Checks))
	}
}

func TestRedactRemovesSecretsAndPersonalPaths(t *testing.T) {
	got := Redact("Authorization: Bearer abcdef0123456789 and sk-abcdefghijklmno AKIA1234567890ABCDEF hf_123456789012345678901 at /Users/alice and /home/bob/code and jane@example.com")
	for _, secret := range []string{"abcdef0123456789", "sk-abcdefghijklmno", "AKIA1234567890ABCDEF", "hf_123456789012345678901", "/Users/alice", "/home/bob", "jane@example.com"} {
		if strings.Contains(got, secret) {
			t.Errorf("secret %q remains in %q", secret, got)
		}
	}
}

func TestRedactRemovesTerminalControlCharacters(t *testing.T) {
	if got := Redact("safe\x1b[31m red"); got != "safe[31m red" {
		t.Fatalf("unexpected terminal text %q", got)
	}
}

func TestDiagnosisAttributesToolFailure(t *testing.T) {
	got := diagnose([]Check{{ID: "tool.basic", Status: "fail"}})
	if len(got) != 1 || got[0].Code != "MODEL_SCHEMA_FAILURE" {
		t.Fatalf("unexpected diagnosis: %#v", got)
	}
}

func TestSafeEndpointRemovesCredentialsAndQuery(t *testing.T) {
	got := safeEndpoint("https://user:secret@example.test/v1?token=abc")
	if got != "https://<provider>/v1" {
		t.Fatalf("unexpected endpoint %q", got)
	}
}

func TestProviderEndpointMustBeHTTP(t *testing.T) {
	for _, value := range []string{"ftp://host.test", "/v1", "http:///missing-host"} {
		if validateEndpoint(value) == nil {
			t.Errorf("accepted invalid endpoint %q", value)
		}
	}
	if err := validateEndpoint("https://api.example.test/v1"); err != nil {
		t.Fatal(err)
	}
}

func TestRateLimitDiagnosisDoesNotPersistProviderBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Bearer private-provider-detail", http.StatusTooManyRequests)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	report, err := Run(ctx, Options{Provider: "openai", Endpoint: server.URL + "/v1", Model: "test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnoses) != 1 || report.Diagnoses[0].Code != "PROVIDER_RATE_LIMIT" {
		t.Fatalf("unexpected diagnosis: %#v", report.Diagnoses)
	}
	if strings.Contains(Text(report), "private-provider-detail") {
		t.Fatal("provider response body was copied into the report")
	}
}
