package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveReportDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte("keep this report"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveReport(path, "new report"); err == nil {
		t.Fatal("saveReport overwrote an existing file")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep this report" {
		t.Fatalf("existing report changed to %q", got)
	}
}

func TestFixCommandPreviewsAndAppliesOnlyToTimeoutReport(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "opencode.json")
	reportPath := filepath.Join(root, "report.json")
	original := []byte(`{"model":"ollama/qwen"}`)
	if err := os.WriteFile(config, original, 0600); err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"harness": "OpenCode", "diagnoses": []map[string]string{{"code": "RUNTIME_TIMEOUT"}}}
	data, _ := json.Marshal(report)
	if err := os.WriteFile(reportPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	output, _ := os.CreateTemp(root, "output")
	errors, _ := os.CreateTemp(root, "errors")
	defer output.Close()
	defer errors.Close()
	args := []string{"fix", "--report", reportPath, "--config", config, "--provider", "ollama"}
	if code := run(args, output, errors); code != 0 {
		t.Fatalf("preview returned %d", code)
	}
	current, _ := os.ReadFile(config)
	if string(current) != string(original) {
		t.Fatal("preview changed the configuration")
	}
	if code := run(append(args, "--apply"), output, errors); code != 0 {
		t.Fatalf("apply returned %d", code)
	}
	backup, err := os.ReadFile(config + ".bak")
	if err != nil || string(backup) != string(original) {
		t.Fatalf("backup %q, %v", backup, err)
	}
	updated, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(updated, &result); err != nil {
		t.Fatal(err)
	}
	if result["provider"].(map[string]any)["ollama"].(map[string]any)["options"].(map[string]any)["timeout"] != float64(600000) {
		t.Fatal("timeout was not applied")
	}
}
