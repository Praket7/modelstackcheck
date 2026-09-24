package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMockProviderDoctorPasses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	report, err := Run(ctx, Options{Provider: "mock", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if report.Provider != "mock" || report.Model != "modeldoctor-mock" {
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
