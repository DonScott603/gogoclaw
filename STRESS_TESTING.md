# Stress Testing

## Highest-risk performance paths

These are the paths most likely to fail first under load in the current codebase:

1. `POST /api/message` in `internal/channel/rest.go`
   - This is the hottest synchronous path. Each request pays for auth, rate limiting, audit logging, optional multipart parsing and file writes, session lookup, provider execution, and SQLite persistence before returning.
2. Session creation and history loading in `internal/engine/session_manager.go`
   - Cold conversations load history from SQLite, while hot conversations concentrate contention on shared sessions and larger in-memory histories.
3. Message persistence in `internal/engine/persistence.go` and `internal/storage/conversations.go`
   - First writes use a transaction that ensures conversation creation and message persistence together. Follow-up requests update message rows and conversation timestamps under the same database.
4. Provider latency amplification in `internal/engine/engine.go` and `internal/provider/router.go`
   - REST calls stay synchronous all the way through provider execution. Retries, timeouts, and tool-call loops directly inflate latency and cut throughput.
5. Context assembly and optional memory lookup in `internal/engine/context.go` and `internal/memory/*`
   - Reused conversations increase history scanning, token counting, and memory enrichment cost.
6. Multipart upload handling in `internal/channel/rest.go`
   - Upload parsing and inbox writes add disk IO to the same latency-sensitive request path.
7. `GET /api/conversations` in `internal/channel/rest.go`
   - Read traffic shares SQLite with write-heavy message traffic while ordering by `updated_at`.

## What was added

- `cmd/stresstest`
  - Standalone Go load runner using only the standard library
  - Targets the existing REST API and exits non-zero when thresholds fail
  - Includes `smoke`, `sustained`, `spike`, and `breakpoint` scenarios
- `testdata/stress/fixtures.json`
  - Weighted realistic message fixtures, including upload-backed requests
- `testdata/stress/uploads/*`
  - Multipart fixtures that exercise the inbox write path
- `stress.env.example`
  - Environment-based configuration for base URL, API key, durations, concurrency, warmup, transport pooling, and thresholds
- `Makefile`
  - Local convenience commands for each scenario

## Scenario defaults

- `smoke`
  - 15s, concurrency 1
  - Verifies `/api/message`, `/api/health`, `/api/conversations`, and multipart uploads with zero tolerated errors
- `sustained`
  - 2m, concurrency 6
  - Exercises steady-state REST, provider, and SQLite pressure
- `spike`
  - 75s, concurrency 12
  - Tests short high-pressure bursts against the same hot path
- `breakpoint`
  - Starts at concurrency 2, increases by 2 every 20s until thresholds fail or concurrency 24 is reached
  - Reports the last passing concurrency level

## Thresholds

All scenarios enforce:

- Latency via `p95` and `p99`
- Throughput via minimum requests per second
- Error rate via maximum failed-request ratio

Defaults are scenario-appropriate and can be overridden with environment variables from [stress.env.example](/C:/Users/donal/Projects/GoGoClaw/stress.env.example).

## Local setup

1. Enable the REST channel in your normal GoGoClaw config and note the API key.
2. Start GoGoClaw so the REST API is listening, usually on `http://127.0.0.1:8080`.
3. Export the values you want from [stress.env.example](/C:/Users/donal/Projects/GoGoClaw/stress.env.example).

PowerShell example:

```powershell
$env:STRESS_BASE_URL="http://127.0.0.1:8080"
$env:STRESS_API_KEY="your-rest-api-key"
```

## Commands

Direct `go run` commands:

```powershell
go run ./cmd/stresstest -scenario smoke
go run ./cmd/stresstest -scenario sustained
go run ./cmd/stresstest -scenario spike
go run ./cmd/stresstest -scenario breakpoint
```

Convenience `make` targets:

```powershell
make stress-smoke
make stress-sustained
make stress-spike
make stress-breakpoint
make stress-all
```

Useful overrides:

```powershell
$env:STRESS_SUSTAINED_CONCURRENCY="10"
$env:STRESS_SUSTAINED_DURATION="3m"
$env:STRESS_THRESHOLD_P95_MS="2500"
go run ./cmd/stresstest -scenario sustained
```

## Expected output

A passing run prints one scenario header, an optional warmup line, a summary line, the active thresholds, endpoint counts, HTTP status counts, transport/setup failure counts, and `STATUS PASS`.

Example:

```text
==> scenario=smoke base_url=http://127.0.0.1:8080 concurrency=1 duration=15s
WARMUP duration=3s
RESULT scenario=smoke total=18 errors=0 error_rate=0.00% active_throughput=1.20rps drain_throughput=1.18rps avg=142.00ms p50=131.00ms p95=220.00ms p99=245.00ms active_duration=15s drain_duration=15.2s bytes=12345
THRESHOLDS p95<=1500.0ms p99<=2000.0ms throughput>=0.50rps error_rate<=0.00%
ENDPOINT_COUNTS conversations=3 health=4 message=11
STATUS_COUNTS 200=18
FAILURE_COUNTS none
STATUS PASS
```

Threshold failures return a non-zero exit code and end with `STATUS FAIL`. Breakpoint runs also print the last passing concurrency:

```text
BREAKPOINT reached at concurrency=14
last_passing_concurrency=12
```

## Notes

- The harness is production-safe because it does not modify application runtime behavior. It only drives the existing REST interface from a separate command.
- Multipart requests intentionally reuse the real upload path so inbox file writes are part of the measurement.
- If provider latency dominates in your environment, the harness will expose that directly in `p95`, `p99`, and throughput.
