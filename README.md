# ModelDoctor

![ModelDoctor mark](docs/images/modeldoctor-mark.svg)

ModelDoctor helps you find where an AI coding setup breaks, so you can fix the right layer. It checks a model through its provider and shows evidence for tool calling, streaming, and recovery.

![A sample ModelDoctor run](docs/images/doctor-report.svg)

## Try it

Download the [latest alpha](https://github.com/Praket7/modeldoctor/releases/latest) for your computer, then run `mdoc doctor`. The first release works with Ollama and OpenAI compatible providers, and includes a local mock provider for a quick demo.

```sh
mdoc doctor
```

For a private demo that needs no model account, run `mdoc doctor --provider mock`. It checks a repeatable example and keeps the report on your computer.

```sh
mdoc doctor --provider mock
```

## What it checks

ModelDoctor checks the provider connection and streaming, then asks for a basic tool call. It also checks nested arguments and multiple tools, so common compatibility problems are easier to spot. A final probe sends a simulated tool error and checks whether the model can continue.

The result includes the checks that passed and the failures that need attention. It also offers a next step, but it never changes your settings or uploads your prompts.

## Connect a provider

Ollama is detected when it is running on its usual local address. For another OpenAI compatible service, set `OPENAI_BASE_URL` and `OPENAI_API_KEY`, then run `mdoc doctor`. You can also set `OPENAI_MODEL` or pass a model name directly.

```sh
export OPENAI_BASE_URL=https://api.example.com/v1
export OPENAI_API_KEY=your-key
export OPENAI_MODEL=your-model
mdoc doctor
```

The mock provider is local and deterministic, so it is useful when you want to check the report format or explore the commands.

## Save a report

Reports can be printed as JSON or Markdown, and you can save either format to a file. Sensitive keys, email addresses, and home folder names are redacted from report text.

```sh
mdoc doctor --format json --output report.json
mdoc doctor --format markdown --output report.md
mdoc diagnose report.json
```

## Current limits

This is an early release, so it tests the provider directly and detects OpenCode when it is present. It does not yet send the same task through OpenCode, inspect long context behavior, test vision, or edit configuration. A passing result describes these probes only, and does not certify a model or coding setup.

## Build from source

Install Go 1.27 or newer, then build the command from this folder.

```sh
go test ./...
go build -o mdoc ./cmd/mdoc
```

## Project

ModelDoctor is open source under the MIT license, and contributions are welcome. Read the [build notes](BUILD.md), [contribution guide](CONTRIBUTING.md), and [security policy](SECURITY.md) before sending a change.

The working name still needs a final availability and trademark review, so this release is marked as an alpha.
