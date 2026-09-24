# Build notes

ModelStackCheck is written in Go and uses the standard library, so it has no extra runtime packages.

## Requirements

Use Go 1.27 or newer on macOS, Windows, or Linux.

## Build

```sh
go test ./...
go vet ./...
go build -o modelstackcheck ./cmd/modelstackcheck
```

On Windows, use `go build -o modelstackcheck.exe ./cmd/modelstackcheck`.

## Try the local example

```sh
go run ./cmd/modelstackcheck doctor --provider mock
```

## Connect a provider

Ollama uses its usual local address, and other compatible providers use `OPENAI_BASE_URL`, `OPENAI_API_KEY`, and optionally `OPENAI_MODEL`. Include `/v1` in the address when the provider expects it.

## Report privacy

The command does not send telemetry or save prompts. It writes a report only when you give it an output path, but you should review that report before sharing because provider errors may include local details.
