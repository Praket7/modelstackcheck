package doctor

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	secretPattern    = regexp.MustCompile(`(?i)(sk-ant-[a-z0-9_-]{12,}|sk-[a-z0-9_-]{12,}|gh[pousr]_[a-z0-9]{20,}|github_pat_[a-z0-9_]{20,}|AIza[a-z0-9_-]{20,}|AKIA[A-Z0-9]{16}|hf_[a-z0-9]{20,}|xox[baprs]-[a-z0-9-]{12,}|Bearer\s+[^\s,;]+)`)
	emailPattern     = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+\b`)
	unixHomePattern  = regexp.MustCompile(`/Users/[^/\s]+`)
	linuxHomePattern = regexp.MustCompile(`/home/[^/\s]+`)
	winHomePattern   = regexp.MustCompile(`(?i)[A-Z]:\\Users\\[^\\\s]+`)
)

func Redact(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 {
			return r
		}
		return -1
	}, value)
	value = secretPattern.ReplaceAllString(value, "<REDACTED>")
	value = emailPattern.ReplaceAllString(value, "<REDACTED>")
	value = unixHomePattern.ReplaceAllString(value, "<HOME>")
	value = linuxHomePattern.ReplaceAllString(value, "<HOME>")
	value = winHomePattern.ReplaceAllString(value, "<HOME>")
	return value
}

func Text(report *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "MODELDOCTOR REPORT\n\nProvider  %s\n", Redact(report.Provider))
	if report.Model != "" {
		fmt.Fprintf(&b, "Model     %s\n", Redact(report.Model))
	}
	if report.Harness != "" {
		fmt.Fprintf(&b, "Harness   %s\n", Redact(report.Harness))
	}
	fmt.Fprintf(&b, "System    %s %s\n\nCHECKS\n", report.System, report.Architecture)
	for _, check := range report.Checks {
		mark := "✓"
		if check.Status == "fail" || check.Status == "error" {
			mark = "✗"
		}
		if check.Status == "warn" || check.Status == "skip" {
			mark = "!"
		}
		fmt.Fprintf(&b, "%s %-25s %s", mark, check.Name, strings.ToUpper(check.Status))
		if check.DurationMS > 0 {
			fmt.Fprintf(&b, " in %d ms", check.DurationMS)
		}
		b.WriteByte('\n')
		if check.Error != "" {
			fmt.Fprintf(&b, "  %s\n", Redact(check.Error))
		}
	}
	if len(report.Diagnoses) > 0 {
		b.WriteString("\nDIAGNOSIS\n")
		for _, diagnosis := range report.Diagnoses {
			fmt.Fprintf(&b, "%s with %s confidence\n", Redact(diagnosis.Summary), strings.ToLower(diagnosis.Confidence))
			fmt.Fprintf(&b, "Evidence  %s\n", strings.Join(redacted(diagnosis.Evidence), ", "))
			fmt.Fprintf(&b, "Try       %s\n", Redact(diagnosis.Recommendation))
		}
	}
	if len(report.Notes) > 0 {
		b.WriteString("\nNOTES\n")
		for _, note := range report.Notes {
			fmt.Fprintf(&b, "%s\n", Redact(note))
		}
	}
	return b.String()
}

func Markdown(report *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# ModelDoctor report\n\nProvider %s  \n", Redact(report.Provider))
	if report.Model != "" {
		fmt.Fprintf(&b, "Model %s  \n", Redact(report.Model))
	}
	if report.Harness != "" {
		fmt.Fprintf(&b, "Harness %s  \n", Redact(report.Harness))
	}
	fmt.Fprintf(&b, "System %s %s\n\n## Checks\n\n", report.System, report.Architecture)
	for _, check := range report.Checks {
		fmt.Fprintf(&b, "### %s\n\nStatus %s\n\n", check.Name, strings.ToUpper(check.Status))
		if check.Evidence != "" {
			fmt.Fprintf(&b, "%s\n\n", Redact(check.Evidence))
		}
		if check.Error != "" {
			fmt.Fprintf(&b, "Observed response %s\n\n", Redact(check.Error))
		}
		if check.DurationMS > 0 {
			fmt.Fprintf(&b, "Observed duration %d ms\n\n", check.DurationMS)
		}
	}
	if len(report.Diagnoses) > 0 {
		b.WriteString("## Diagnosis\n\n")
		for _, diagnosis := range report.Diagnoses {
			fmt.Fprintf(&b, "%s\n\nConfidence %s\n\nEvidence %s\n\nTry %s\n\n", Redact(diagnosis.Summary), strings.ToLower(diagnosis.Confidence), strings.Join(redacted(diagnosis.Evidence), ", "), Redact(diagnosis.Recommendation))
		}
	}
	if len(report.Notes) > 0 {
		b.WriteString("## Notes\n\n")
		for _, note := range report.Notes {
			fmt.Fprintf(&b, "%s\n\n", Redact(note))
		}
	}
	return b.String()
}

func redacted(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = Redact(value)
	}
	return out
}
