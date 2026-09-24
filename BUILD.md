# Build notes

ModelDoctor uses Go and the standard library, so the CLI has no runtime dependency to install.

## Requirements

Use Go 1.27 or newer on macOS, Windows, or Linux.

## Build

```sh
go test ./...
go vet ./...
go build -o mdoc ./cmd/mdoc
```

On Windows, use `go build -o mdoc.exe ./cmd/mdoc`.

## Try the local example

```sh
go run ./cmd/mdoc doctor --provider mock
```

## Provider settings

Ollama is checked at its local OpenAI compatible endpoint. Other compatible endpoints use `OPENAI_BASE_URL`, `OPENAI_API_KEY`, and optionally `OPENAI_MODEL`. The endpoint path should include `/v1` when the provider expects it.

## Report privacy

The CLI does not send telemetry or save prompts. Reports are written only when an output path is provided. Review a report before sharing it, since provider errors can contain details unique to a local service.
