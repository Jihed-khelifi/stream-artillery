# Coding Style Guide (concise)

This repository follows a small set of intentional, opinionated conventions. The points below highlight the codebase-specific and less-common patterns you should follow. (Common best practices — e.g. "use defer to close resources" — are intentionally omitted.)

---

## Project-level conventions
- Keep CLI and app wiring in `main.go` and keep helper concerns (stats, reporting, request/IO helpers) in the same file unless they grow large.
- Small readable constants (e.g. `defaultJSONBody`) live at top-level as `const` strings.

---

## Stats and concurrency state
- Use the new atomic value types (e.g. `atomic.Int64`) as struct fields for counters:
  - Increment: `stats.totalRequests.Add(1)`
  - Read: `stats.totalRequests.Load()`
- Use a separate `sync.Mutex` on the same struct for non-atomic, compound operations (file logging, formatted multi-step writes):
  - Example: `stats.mu.Lock()` → write to `stats.errorLogger` → `stats.mu.Unlock()`.
- Keep a `startTime time.Time` on the stats struct for runtime calculations.

---

## Error logging
- Maintain a `log.Logger` backed by an `*os.File` on the `Stats` struct:
  - Create with `log.New(errorFile, "", log.LstdFlags)` and use both for persistent logging and stdout reporting.
- Always serialize multi-step logging with `stats.mu` to avoid interleaved log lines.
- Error output style: log to file and also print to stdout with a visible symbol:
  - Success/complete messages use ✓, error lines use ✗.
- Use a compact `logError(stats, requestID, message, err)` helper that formats `[ID] message: <err>`.

---

## HTTP client pooling and selection
- Support two modes:
  - hcc == 0: create a fresh `http.Client` per request (clients slice is `nil`).
  - hcc > 0: pre-create a slice of `*http.Client` and reuse them.
- Configure clients with an explicit `Timeout` and tuned `Transport`:
  - `MaxIdleConns`, `MaxIdleConnsPerHost`, `IdleConnTimeout` are explicitly set.
- Round-robin selection across the pre-created clients using a deterministic formula:
  - `clientIndex := (workerID*hits + hitID) % len(clients)`

---

## Concurrency patterns
- Worker pattern:
  - Spawn `concurrency` workers; each worker spawns `hits` goroutines simultaneously and waits on an inner `sync.WaitGroup`.
  - Intentionally _no sleeps_ between hits to stress simultaneous requests.
- Use a top-level `context.Context` (e.g. `ctx, cancel := context.WithCancel(context.Background())`) and pass it to requests with `http.NewRequestWithContext`.
- Use `sync.WaitGroup` to coordinate worker lifetime, and a small reporting goroutine that uses a ticker.

---

## Streaming request handling
- Use `bufio.NewReader(resp.Body)` and `ReadBytes('\n')` to process SSE-style / line-delimited streams.
- Terminate reading when:
  - `io.EOF` → stream ended successfully
  - a line contains the sentinel string (example): `bytes.Contains(chunk, []byte("data: [DONE]"))`
- Increment per-chunk metrics as chunks are read:
  - `stats.totalChunks.Add(1)` and `stats.totalBytes.Add(int64(len(chunk)))`
- Treat HTTP non-200 statuses as errors, and increment `maxConcurrentErrors` for throttling / 503 / 429 cases.

---

## Request & headers
- Content-type and SSE headers for streaming endpoints:
  - `Content-Type: application/json`
  - `Accept: text/event-stream; charset=utf-8`
  - `Connection: keep-alive`
- Build request body as `bytes.NewBufferString(requestBody)` and pass the context-aware request to `client.Do`.

---

## CLI / JSON body handling
- `jsonBody` flag accepts:
  - empty → use `defaultJSONBody`
  - a path ending in `.json` → expand `~` to the home directory and read/validate file content
  - a JSON string → validate the string with `json.Unmarshal([]byte(input), &var)` before use
- Use `strings.HasSuffix(input, ".json")` to decide file-vs-string, and `strings.TrimSpace` on inputs.

---

## IDs, naming & formatting
- Per-request ID pattern: `W%d-H%d` (worker-hit). Use that for logging and readable stdout lines.
- Use concise camelCase variable names. Functions are short, well-scoped, and ordered in file for readability:
  - main → runWorker → makeStreamingRequest → logError → reportStats → formatBytes → loadJSONBody
- Reporting messages should be compact and human-readable (include ticks/crosses and summary info).

---

## Output & reporting
- Live reporting via a ticker (5s interval) printing Totals/Success/Failed/Chunks/Bytes/RPS.
- Final summary prints Duration, Totals, Success/Fail percentages, MaxConcurrentErrors, chunk & byte totals, average chunks per request, and requests/sec.

---

## Utility helpers & minor idioms
- formatBytes uses a unit loop with `div` and `exp`, indexing into the string `"KMGTPE"` for units: `fmt.Sprintf("%.2f %cB", value, "KMGTPE"[exp])`.
- When expanding `~` in paths, call `os.UserHomeDir()` and reconstruct with `filepath.Join(home, input[1:])`.
- Validate JSON via `json.Unmarshal` even when reading from a file (fail-fast on invalid input).

---

Follow these conventions to keep behavior consistent across workers, clients, logging and streaming handling.