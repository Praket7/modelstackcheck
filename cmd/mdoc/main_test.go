package main

import (
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
