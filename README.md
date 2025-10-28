# Stream Artillery

A load testing CLI tool for stress testing streaming HTTP endpoints.

## Usage

```bash
go run main.go --url <target-url> [options]
```

### Options

- `--url` - Target URL (required)
- `--w` - Number of concurrent workers (default: 3)
- `--hits` - Requests per worker (default: 1)
- `--hcc` - HTTP clients to create (default: 0, creates new client per request)
- `--jsonBody` - JSON body string or path to .json file (optional)

### Example

```bash
go run main.go --url http://localhost:4000/chat/completions --w 10 --hits 5 --hcc 6
```

This sends 50 total requests (10 workers × 5 hits) using 6 shared HTTP clients.

## Output

Results are displayed in the terminal with stats on throughput, latency, and errors. Failed requests are logged to `errors.log`.
