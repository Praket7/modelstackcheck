# Contributing

Thanks for helping make AI coding setups easier to diagnose. Small, reproducible changes are especially useful, and reports should include only the evidence needed to explain a failure.

## Before you start

Open an issue for a large change, so we can agree on its purpose and scope. Do not include API keys, private prompts, or customer code in issues.

## Development

Use the Go standard library where it fits, and keep provider behavior covered by local fixtures. Before sending a change, run the checks below.

```sh
go test ./...
go vet ./...
go build ./cmd/modelstackcheck
```

## Pull requests

Describe the user problem and the evidence for the change. Include any security impact, and update the documentation when the command behavior changes.
