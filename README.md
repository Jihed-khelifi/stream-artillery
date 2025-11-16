# Stream Artillery

## Overview

**Stream Artillery** is a Go library and CLI tool for making HTTP streaming requests to SSE-style endpoints. The library provides:

- **Reusable `stream` package**: Execute streaming HTTP requests with flexible stop conditions (content match, byte limit, chunk limit), optional metrics recording, and per-stream progress observers.
- **Example CLI program**: Demonstrates library usage with several scenarios and per-chunk progress logging.

The core streaming logic is designed to be importable and reusable in other Go projects, while the example program (`main.go`) shows how to integrate it.

## Installation

Add the module to your Go project:

```bash
go get stream-artillery
```

Import the package in your code:

```go
import "stream-artillery/stream"
```

## Library Usage

### Basic Streaming with Metrics

```go
package main

import (
    "context"
    "fmt"
    "net/http"
    "sync/atomic"
    "time"

    "stream-artillery/stream"
)

type Stats struct {
    chunks atomic.Int64
    bytes  atomic.Int64
}

func (s *Stats) AddTotalChunks(n int64) {
    s.chunks.Add(n)
}

func (s *Stats) AddTotalBytes(n int64) {
    s.bytes.Add(n)
}

func main() {
    client := &http.Client{
        Timeout: 30 * time.Second,
    }

    stats := &Stats{}
    condition := &stream.ContentMatchCondition{Pattern: "data: [DONE]"}

    result := stream.ExecuteStream(
        context.Background(),
        "http://localhost:4000",
        `{"messages": [{"role": "user", "content": "Hello"}], "stream": true}`,
        client,
        condition,
        stats,
    )

    if result.Err != nil {
        fmt.Printf("Error: %v\n", result.Err)
        return
    }

    fmt.Printf("Success: %d chunks, %d bytes in %.2fs\n",
        result.ChunkCount, result.TotalBytes, result.Duration.Seconds())
    fmt.Printf("Total stats: %d chunks, %d bytes\n",
        stats.chunks.Load(), stats.bytes.Load())
}
```

### Progress Tracking with Observer

```go
type ProgressObserver struct {
    requestID string
}

func (o *ProgressObserver) OnChunk(chunk []byte, chunkCount int, totalBytes int64, elapsed time.Duration) {
    fmt.Printf("[%s] Chunk %d: %d bytes (total: %d bytes, %.2fs)\n",
        o.requestID, chunkCount, len(chunk), totalBytes, elapsed.Seconds())
}

func (o *ProgressObserver) OnComplete(result stream.StreamResult) {
    if result.Err != nil {
        fmt.Printf("[%s] Failed: %v\n", o.requestID, result.Err)
    } else {
        fmt.Printf("[%s] Complete: %d chunks, %d bytes\n",
            o.requestID, result.ChunkCount, result.TotalBytes)
    }
}

// Usage
observer := &ProgressObserver{requestID: "req-1"}
result := stream.ExecuteStreamWithObserver(
    ctx, url, body, client, condition, metrics, observer,
)
```

## Stop Conditions

The library supports three types of stop conditions:

### Content Match

Stop when a chunk contains a specific pattern:

```go
condition := &stream.ContentMatchCondition{Pattern: "data: [DONE]"}
```

### Byte Limit

Stop after receiving a certain number of bytes:

```go
condition := &stream.ByteLimitCondition{Limit: 1024}
```

### Chunk Limit

Stop after receiving a certain number of chunks:

```go
condition := &stream.ChunkLimitCondition{Limit: 10}
```

### Dynamic Condition from Flags

Parse conditions from string parameters (useful for CLI flags or configuration):

```go
condition, err := stream.NewStopConditionFromFlags("bytes", "1024")
if err != nil {
    // handle error
}
```

Supported types: `"content"`, `"bytes"`, `"chunks"`.

## CLI / Example Program

The repository includes an example CLI program in `main.go` that demonstrates library usage:

- Makes streaming requests against `http://localhost:4000` by default
- Demonstrates multiple scenarios: content match, byte limit, chunk limit, and concurrent requests
- Prints per-chunk progress using `StreamObserver`

### Running the Example

Start an SSE-capable server on `http://localhost:4000`, then run:

```bash
go run .
```

The program will execute four examples:
1. Content match stop condition
2. Byte limit stop condition
3. Chunk limit stop condition
4. Multiple concurrent streams

**Note**: The `internal/cli` package contains an animated terminal dashboard (using ANSI control codes) that provides a richer visualization option. The current `main.go` demonstrates simpler per-chunk logging, but the animated display could be integrated for more complex CLI tools.

## Running Tests

Run all tests:

```bash
go test ./...
```

The test suite includes:
- Unit tests for stop conditions and streaming logic
- Integration tests with mock SSE servers
- Concurrent streaming scenarios
- Error handling and timeout cases

## Package Structure

- **`stream/`** - Public API package (import this in your projects)
- **`internal/stream/`** - Core streaming implementation
- **`internal/cli/`** - Animated terminal display helpers
- **`main.go`** - Example CLI program

## License

See LICENSE file for details.
