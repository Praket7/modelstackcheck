package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Praket7/modelstackcheck/internal/doctor"
)

var version = "0.1.2-alpha"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut *os.File) int {
	if len(args) == 0 {
		usage(out)
		return 0
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(out, "ModelStackCheck", version)
		return 0
	case "help", "-h", "--help":
		usage(out)
		return 0
	case "doctor":
		return runDoctor(args[1:], out, errOut)
	case "diagnose":
		return runDiagnose(args[1:], out, errOut)
	case "mock-provider":
		return runMock(args[1:], out, errOut)
	case "fix":
		return runFix(args[1:], out, errOut)
	default:
		fmt.Fprintf(errOut, "Unknown command %q\n\n", args[0])
		usage(errOut)
		return doctor.ExitConfiguration
	}
}

func usage(out *os.File) {
	fmt.Fprintln(out, `ModelStackCheck finds where an AI coding setup breaks.

Usage
  modelstackcheck doctor [options]
  modelstackcheck diagnose report.json
  modelstackcheck fix [options]
  modelstackcheck mock-provider serve
  modelstackcheck version

Doctor options
  --provider auto, ollama, openai, or mock
  --model model name for the provider
  --endpoint provider address
  --harness auto or opencode
  --profile quick, context, vision, or full
  --format text, markdown, or json
  --output save a report to a file
  --timeout maximum time for each request

OpenAI compatible providers read OPENAI_API_KEY, OPENAI_BASE_URL, and OPENAI_MODEL.
Reports stay on this computer unless you save or share them.`)
}

func runDoctor(args []string, out, errOut *os.File) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(errOut)
	providerName := fs.String("provider", "auto", "provider to inspect")
	model := fs.String("model", "", "model name")
	endpoint := fs.String("endpoint", "", "provider address")
	harness := fs.String("harness", "auto", "coding harness to detect")
	format := fs.String("format", "text", "text, markdown, or json")
	output := fs.String("output", "", "report output path")
	timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
	profile := fs.String("profile", "quick", "quick, context, vision, or full")
	if err := fs.Parse(args); err != nil {
		return doctor.ExitConfiguration
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errOut, "doctor does not accept positional arguments")
		return doctor.ExitConfiguration
	}
	if *timeout <= 0 {
		fmt.Fprintln(errOut, "timeout must be greater than zero")
		return doctor.ExitConfiguration
	}
	if *format != "text" && *format != "markdown" && *format != "json" {
		fmt.Fprintln(errOut, "format must be text, markdown, or json")
		return doctor.ExitConfiguration
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	report, err := doctor.Run(ctx, doctor.Options{Provider: *providerName, Model: *model, Endpoint: *endpoint, Harness: *harness, Profile: *profile, Timeout: *timeout})
	if err != nil {
		fmt.Fprintln(errOut, doctor.Redact(err.Error()))
		return doctor.ExitConfiguration
	}
	var body string
	switch *format {
	case "json":
		data, e := json.MarshalIndent(report, "", "  ")
		if e != nil {
			fmt.Fprintln(errOut, e)
			return doctor.ExitInternal
		}
		body = string(data) + "\n"
	case "markdown":
		body = doctor.Markdown(report)
	default:
		body = doctor.Text(report)
	}
	if *output != "" {
		if err := saveReport(*output, body); err != nil {
			fmt.Fprintf(errOut, "Could not save report: %s\n", doctor.Redact(err.Error()))
			return doctor.ExitConfiguration
		}
		fmt.Fprintf(out, "Report saved to %s\n", doctor.Redact(*output))
	} else {
		fmt.Fprint(out, body)
	}
	return report.ExitCode()
}

func runFix(args []string, out, errOut *os.File) int {
	fs := flag.NewFlagSet("fix", flag.ContinueOnError)
	fs.SetOutput(errOut)
	reportPath := fs.String("report", "", "diagnostic report with a provider timeout finding")
	config := fs.String("config", "opencode.json", "OpenCode JSON configuration file")
	provider := fs.String("provider", "", "OpenCode provider name")
	timeout := fs.Duration("timeout", 10*time.Minute, "new provider request timeout")
	apply := fs.Bool("apply", false, "apply the fix and create a backup")
	if err := fs.Parse(args); err != nil {
		return doctor.ExitConfiguration
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errOut, "fix does not accept positional arguments")
		return doctor.ExitConfiguration
	}
	if *reportPath == "" {
		fmt.Fprintln(errOut, "fix needs --report from a doctor run")
		return doctor.ExitConfiguration
	}
	reportData, err := os.ReadFile(*reportPath)
	if err != nil {
		fmt.Fprintln(errOut, "Could not read the diagnostic report")
		return doctor.ExitConfiguration
	}
	var report doctor.Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		fmt.Fprintln(errOut, "That file is not a ModelStackCheck report")
		return doctor.ExitConfiguration
	}
	if report.Harness != "OpenCode" {
		fmt.Fprintln(errOut, "The report did not identify OpenCode")
		return doctor.ExitConfiguration
	}
	fixable := false
	for _, diagnosis := range report.Diagnoses {
		if diagnosis.Code == "RUNTIME_TIMEOUT" {
			fixable = true
		}
	}
	if !fixable {
		fmt.Fprintln(errOut, "The report has no supported provider timeout finding")
		return doctor.ExitConfiguration
	}
	result, err := doctor.FixProviderTimeout(*config, *provider, *timeout, *apply)
	if err != nil {
		fmt.Fprintln(errOut, doctor.Redact(err.Error()))
		return doctor.ExitConfiguration
	}
	fmt.Fprintln(out, doctor.Redact(result))
	return doctor.ExitOK
}

func saveReport(path, body string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(file, body); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	return file.Close()
}

func runDiagnose(args []string, out, errOut *os.File) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, "Usage: modelstackcheck diagnose report.json")
		return doctor.ExitConfiguration
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(errOut, doctor.Redact(err.Error()))
		return doctor.ExitConfiguration
	}
	var report doctor.Report
	if err := json.Unmarshal(data, &report); err != nil {
		fmt.Fprintln(errOut, "That file is not a ModelStackCheck report")
		return doctor.ExitConfiguration
	}
	fmt.Fprint(out, doctor.Text(&report))
	return report.ExitCode()
}

func runMock(args []string, out, errOut *os.File) int {
	if len(args) != 1 || args[0] != "serve" {
		fmt.Fprintln(errOut, "Usage: modelstackcheck mock-provider serve")
		return doctor.ExitConfiguration
	}
	if err := doctor.ServeMock("127.0.0.1:11435"); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(errOut, doctor.Redact(err.Error()))
		return doctor.ExitInternal
	}
	return 0
}
