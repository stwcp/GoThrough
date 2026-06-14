# GoThrough

GoThrough is a lightweight, programmable HTTP reverse proxy library for Go. It compiles to a standard `http.Handler` and supports request mutation, response interception, and passive stream tapping with configurable backpressure.

The engine uses `sync.Pool` for buffer reuse to reduce allocation pressure during high-concurrency streaming workloads.

## Features

- **Interceptors** — mutate requests or short-circuit with an immediate response before the upstream call.
- **Tappers** — passively observe request and response streams (including SSE) on background goroutines.
- **Backpressure strategies** — control how slow tappers interact with the client stream (`Block`, `Drop`, `Unbounded`).
- **Lifecycle metadata** — thread-safe, context-scoped key/value storage shared across interceptors and tappers.
- **Pooled bodies** — request payloads are captured once and reused for upstream forwarding.

## Installation

```bash
go get github.com/stwcp/GoThrough
```

## Backpressure strategies

Passive response tappers read from a decoupled async buffer. When a tapper is slower than the upstream stream, the strategy decides whether the client waits, telemetry is dropped, or memory grows.

| Strategy | Behavior | Best for | Tradeoff |
| --- | --- | --- | --- |
| `StrategyBlock` | Blocks the client stream when the tap buffer is full. | Local debugging, tightly coupled sidecars. | Telemetry completeness over client latency. |
| `StrategyDrop` | Drops tap chunks when the buffer is full. | High-throughput APIs, metrics and logs. | Client latency protected; telemetry may be lost under load. |
| `StrategyUnbounded` | Grows an internal queue so `Write` never waits on the tapper. | Compliance auditing, billing logs. | Full capture at the cost of temporary memory growth. |

Configure globally on the pipeline:

```go
p := proxy.New("https://api.openai.com").
    WithTapStrategy(proxy.StrategyDrop, 128)
```

## Quick start

```go
package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/stwcp/GoThrough/proxy"
)

type PathMatcher struct{ Prefix string }

func (m *PathMatcher) Matches(req *http.Request) bool {
	return strings.HasPrefix(req.URL.Path, m.Prefix)
}

type TelemetryLogger struct{}

func (tl *TelemetryLogger) TapRequest(ctx context.Context, req *http.Request) {
	log.Printf("[TAP REQ] %s %s", req.Method, req.URL.Path)
}

func (tl *TelemetryLogger) TapResponse(ctx context.Context, code int, _ http.Header, body io.Reader) {
	buf := make([]byte, 1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			log.Printf("[TAP RES CHUNK] %d bytes", n)
		}
		if err != nil {
			break
		}
	}
}

type SecurityGuardrail struct{ RequiredHeader string }

func (sg *SecurityGuardrail) InterceptRequest(req *http.Request) (*http.Response, error) {
	if req.Header.Get(sg.RequiredHeader) == "" {
		resp := &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`{"error":"missing required header"}`)),
			Header:     make(http.Header),
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp, nil
	}
	req = proxy.SetMetadata(req, "client-validation-status", "PASSED")
	return nil, nil
}

func main() {
	p := proxy.New("https://api.openai.com").
		WithTapStrategy(proxy.StrategyDrop, 128)

	logger := &TelemetryLogger{}

	p.OnRequest(&PathMatcher{Prefix: "/v1/chat"}).
		Intercept(&SecurityGuardrail{RequiredHeader: "X-Corporate-Token"}).
		Tap(logger)

	p.OnResponse(&PathMatcher{Prefix: "/v1/chat"}).
		Tap(logger)

	log.Println("GoThrough serving on :8080")
	log.Fatal(http.ListenAndServe(":8080", p.Handler()))
}
```

A runnable copy lives in [`examples/reference/main.go`](examples/reference/main.go).

### Backpressure examples

Both examples embed a mock SSE upstream so you can run them without an external API key.

| Example | Command | Strategy | Ports |
| --- | --- | --- | --- |
| Reference (intercept + tap) | `make example` or `make example reference` | default (`StrategyBlock`) | `:8080` |
| Telemetry / metrics | `make example telemetry-drop` | `StrategyDrop` (buffer 8) | proxy `:8080`, upstream `:9090` |
| Compliance audit | `make example compliance-unbounded` | `StrategyUnbounded` (buffer 16) | proxy `:8081`, upstream `:9091` |

After starting either example, stream through the proxy:

```bash
curl -N http://127.0.0.1:8080/stream   # telemetry-drop
curl -N http://127.0.0.1:8081/stream   # compliance-unbounded
```

With `StrategyDrop`, the slow metrics tapper may skip chunks once its buffer fills; the `curl` stream still completes at full speed. With `StrategyUnbounded`, the audit tapper receives every chunk even when it falls behind, at the cost of temporary in-memory growth.

Sources: [`examples/telemetry-drop/main.go`](examples/telemetry-drop/main.go), [`examples/compliance-unbounded/main.go`](examples/compliance-unbounded/main.go).

## Architecture notes

**Stream splitting.** Streaming responses are duplicated with `io.TeeReader` into async tap sinks. The client path reads from the primary stream; tappers consume from a separate buffer according to the configured strategy.

**Tap isolation.** Request tappers receive a metadata snapshot so async reads do not race with interceptors on the main path. Panics inside tappers are recovered and logged; remaining tap data is drained to `io.Discard`.

**Body lifecycle.** Incoming request bodies are read once into a refcounted pooled buffer, reused for interceptors and upstream forwarding, and returned to the pool when the request completes.

## Development

### First-time setup

```bash
make init        # install pinned Go tools into ./tools
make git-hooks   # optional: use .githooks when present
```

Run `make init` before `make check` or `make fmt`. Those targets call `golangci-lint` from `./tools`, not your global install.

### Makefile

The Makefile keeps local development and CI aligned on the same tool versions and commands.

| Target | What it does |
| --- | --- |
| `make` / `make check` | `golangci-lint run`, race tests, and `go build ./...`. Default quality gate. |
| `make init` | `go mod tidy` / `go mod download`, then installs pinned tools into `./tools`. |
| `make test` | `go test -race -count=1 ./...` — tests only, no lint. |
| `make fmt` | `golangci-lint fmt` — format sources without running the full check. |
| `make example [name]` | Run an example (`reference`, `telemetry-drop`, `compliance-unbounded`). Defaults to `reference`. |
| `make test-coverage` | Race tests with a coverage report under `internal/test/coverage/`. |

`go mod tidy` runs as part of `make init`. There is no separate `tidy`, `clean`, or `run` target — this repo is a library, not a deployable service.

**Why pin tools in `./tools`?** Go module versions and linter versions drift independently. Installing tools into the repo and pointing the editor at them avoids "works on my machine" gaps between local runs and CI.

### VS Code / Cursor (`.vscode`)

Workspace settings live in [`.vscode/settings.json`](.vscode/settings.json). They exist so the editor uses the same Go toolchain and repo-local tools as the Makefile.

- **`go.toolsEnvVars.GOTOOLCHAIN`** — pins Go `1.26.2`, matching [`go.mod`](go.mod).
- **`go.alternateTools`** — after `make init`, `gopls`, `dlv`, `golangci-lint`, `goimports`, and `govulncheck` resolve from `./tools` instead of global paths.
- **`go.formatTool` / `gopls.local`** — `goimports` with `-local github.com/stwcp/GoThrough` groups stdlib, third-party, and module imports consistently.
- **`terminal.integrated.env.osx.PATH`** — prepends `mise` and `cargo` shims so integrated terminals match common local toolchains.
- **File nesting** — nests `*_test.go` files under their source `.go` file in the explorer.

[`.vscode/extensions.json`](.vscode/extensions.json) recommends the CodeLLDB extension for Delve-based debugging with the pinned `./tools/dlv` binary.

### Cursor (`.cursor`)

[`.cursor/rules/plaindev.mdc`](.cursor/rules/plaindev.mdc) is an always-on Cursor rule that shapes AI assistant output: short sentences, plain words, and predictable section layouts. It helps when you want scannable answers while working in this repo. Say "stop plaindev" in chat to turn it off for the session.

## License

See repository license file.
