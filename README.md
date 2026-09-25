# ModelStackCheck

![Generated illustration showing a model connected to a provider and a coding tool](docs/images/modelstackcheck-overview.jpg)

ModelStackCheck finds where an AI coding setup breaks and points you toward the next useful check. It tests the provider directly, then it can send a safe fixture through OpenCode so you can see whether the full path works.

![Generated illustration of provider, tool call, file edit, context, and image checks](docs/images/modelstackcheck-diagnostic-path.jpg)

![Sample report from a local ModelStackCheck run](docs/images/doctor-report.svg)

## Download version 0.1.3 alpha

Choose the download that matches your system and processor. Each archive contains the command line program for that platform. The [release page](https://github.com/Praket7/modelstackcheck/releases/tag/v0.1.3-alpha) also includes SHA 256 checksums.

| System | Processor | Download |
| --- | --- | --- |
| macOS | Apple silicon | [Download](https://github.com/Praket7/modelstackcheck/releases/download/v0.1.3-alpha/modelstackcheck_v0.1.3-alpha_darwin_arm64.tar.gz) |
| macOS | Intel | [Download](https://github.com/Praket7/modelstackcheck/releases/download/v0.1.3-alpha/modelstackcheck_v0.1.3-alpha_darwin_amd64.tar.gz) |
| Linux | Intel or AMD 64 bit | [Download](https://github.com/Praket7/modelstackcheck/releases/download/v0.1.3-alpha/modelstackcheck_v0.1.3-alpha_linux_amd64.tar.gz) |
| Linux | ARM 64 bit | [Download](https://github.com/Praket7/modelstackcheck/releases/download/v0.1.3-alpha/modelstackcheck_v0.1.3-alpha_linux_arm64.tar.gz) |
| Windows | Intel or AMD 64 bit | [Download](https://github.com/Praket7/modelstackcheck/releases/download/v0.1.3-alpha/modelstackcheck_v0.1.3-alpha_windows_amd64.tar.gz) |

After downloading, open a terminal in the folder containing the archive and extract it with this command.

```sh
tar -xzf modelstackcheck_v0.1.3-alpha_darwin_arm64.tar.gz
```

Use the archive name that matches your download. On Windows, extract the archive with File Explorer or 7 Zip. The program inside is named `modelstackcheck_v0.1.3-alpha_windows_amd64.exe`.

On macOS or Linux, run a private demo with the local mock provider. For another system, replace the program name with the one inside its archive.

```sh
./modelstackcheck_v0.1.3-alpha_darwin_arm64 doctor --provider mock
```

The mock provider runs on your computer and needs no model account. To see the installed version, run the program with the `version` command.

## What it checks

The quick profile checks the provider connection, streaming, basic and nested tool calls, multiple calls, recovery after a tool error, a file edit in a temporary project, and a restricted file listing.

Add `--harness opencode` to send the same fixture through the installed OpenCode command. ModelStackCheck creates a temporary project, asks the model to read and edit a fixture file, checks the result, and removes the project afterward.

Add `--profile context` to check retrieval at about 16K and 32K tokens. Each check runs three times, and token counts are estimates based on four characters per token. Hosted providers may charge for these requests.

Add `--profile vision` to send a generated red image and check whether the model can identify it. Add `--profile full` to run both context and image checks.

```sh
./modelstackcheck_v0.1.3-alpha_darwin_arm64 doctor --harness opencode --profile full
```

The mock scenarios can show how a failed tool call, an OpenCode parser problem, a timeout, or a context limit appears in the report.

```sh
./modelstackcheck_v0.1.3-alpha_darwin_arm64 doctor --provider mock --mock-scenario malformed-tool
./modelstackcheck_v0.1.3-alpha_darwin_arm64 doctor --provider mock --harness opencode --mock-scenario harness-parser
./modelstackcheck_v0.1.3-alpha_darwin_arm64 doctor --provider mock --profile context --mock-scenario context-degradation
```

## Connect a model provider

Ollama is detected at its usual local address. Other services that use the OpenAI compatible API can be set up with these environment values.

```sh
export OPENAI_BASE_URL=https://api.example.com/v1
export OPENAI_API_KEY=your-key
export OPENAI_MODEL=your-model
./modelstackcheck_v0.1.3-alpha_darwin_arm64 doctor
```

ModelStackCheck sends prompts only to the provider you select. It does not save prompts or upload reports. Review a report before sharing because provider errors can include local details.

## Save and read reports

Reports can be shown in the terminal or saved as JSON and Markdown. Keys, email addresses, and home folder names are redacted from report text.

```sh
./modelstackcheck_v0.1.3-alpha_darwin_arm64 doctor --format json --output report.json
./modelstackcheck_v0.1.3-alpha_darwin_arm64 doctor --format markdown --output report.md
./modelstackcheck_v0.1.3-alpha_darwin_arm64 diagnose report.json
```

## Apply a supported fix

ModelStackCheck can suggest a provider timeout change when an OpenCode report includes that finding. It shows a preview first, and it changes the configuration only when you add `--apply`. The original file is saved beside the configuration as a backup.

```sh
./modelstackcheck_v0.1.3-alpha_darwin_arm64 fix --report report.json --config opencode.json --provider ollama
./modelstackcheck_v0.1.3-alpha_darwin_arm64 fix --report report.json --config opencode.json --provider ollama --apply
```

Fixes support standard JSON files. JSON with comments is left untouched, and an existing backup is never overwritten.

## Build from source

Install Go 1.27 or newer, then run these commands from the project folder.

```sh
go test ./...
go vet ./...
go build -o modelstackcheck ./cmd/modelstackcheck
```

## Project status

Version 0.1.3 alpha is an early preview. The OpenCode path has been checked with a deterministic local provider, and real provider behavior can vary. Context sizes are estimates, and the vision profile checks one simple generated image. A passing report covers only the requests that were tested, so it cannot certify a model for every project.

ModelStackCheck is available under the MIT license. Read the [contribution guide](CONTRIBUTING.md) before proposing a change, and use the [security policy](SECURITY.md) to report a security issue privately.
