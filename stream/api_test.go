package stream_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"stream-artillery/stream"
)

type testMetrics struct {
	chunks atomic.Int64
	bytes  atomic.Int64
}

func (m *testMetrics) AddTotalChunks(n int64) {
	m.chunks.Add(n)
}

func (m *testMetrics) AddTotalBytes(n int64) {
	m.bytes.Add(n)
}

type testObserver struct {
	chunkCalls    int
	completeCalls int
	lastChunk     []byte
	lastResult    stream.StreamResult
}

func (o *testObserver) OnChunk(chunk []byte, chunkCount int, totalBytes int64, elapsed time.Duration) {
	o.chunkCalls++
	o.lastChunk = make([]byte, len(chunk))
	copy(o.lastChunk, chunk)
}

func (o *testObserver) OnComplete(result stream.StreamResult) {
	o.completeCalls++
	o.lastResult = result
}

func TestNewStopConditionFromFlags_Content(t *testing.T) {
	condition, err := stream.NewStopConditionFromFlags("content", "data: [DONE]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if condition == nil {
		t.Fatal("expected non-nil condition")
	}

	if !condition.ShouldStop([]byte("data: [DONE]"), 0, 0) {
		t.Error("expected condition to stop on matching content")
	}

	if condition.ShouldStop([]byte("data: other"), 0, 0) {
		t.Error("expected condition not to stop on non-matching content")
	}
}

func TestNewStopConditionFromFlags_ContentEmpty(t *testing.T) {
	_, err := stream.NewStopConditionFromFlags("content", "")
	if err == nil {
		t.Error("expected error for empty content pattern")
	}
}

func TestNewStopConditionFromFlags_Bytes(t *testing.T) {
	condition, err := stream.NewStopConditionFromFlags("bytes", "1024")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if condition == nil {
		t.Fatal("expected non-nil condition")
	}

	if condition.ShouldStop(nil, 1023, 0) {
		t.Error("expected condition not to stop at 1023 bytes")
	}

	if !condition.ShouldStop(nil, 1024, 0) {
		t.Error("expected condition to stop at 1024 bytes")
	}

	if !condition.ShouldStop(nil, 2000, 0) {
		t.Error("expected condition to stop at 2000 bytes")
	}
}

func TestNewStopConditionFromFlags_BytesInvalid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"non-numeric", "abc"},
		{"negative", "-100"},
		{"zero", "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := stream.NewStopConditionFromFlags("bytes", tt.value)
			if err == nil {
				t.Errorf("expected error for value %q", tt.value)
			}
		})
	}
}

func TestNewStopConditionFromFlags_Chunks(t *testing.T) {
	condition, err := stream.NewStopConditionFromFlags("chunks", "10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if condition == nil {
		t.Fatal("expected non-nil condition")
	}

	if condition.ShouldStop(nil, 0, 9) {
		t.Error("expected condition not to stop at 9 chunks")
	}

	if !condition.ShouldStop(nil, 0, 10) {
		t.Error("expected condition to stop at 10 chunks")
	}

	if !condition.ShouldStop(nil, 0, 15) {
		t.Error("expected condition to stop at 15 chunks")
	}
}

func TestNewStopConditionFromFlags_ChunksInvalid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"non-numeric", "xyz"},
		{"negative", "-5"},
		{"zero", "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := stream.NewStopConditionFromFlags("chunks", tt.value)
			if err == nil {
				t.Errorf("expected error for value %q", tt.value)
			}
		})
	}
}

func TestNewStopConditionFromFlags_UnknownType(t *testing.T) {
	_, err := stream.NewStopConditionFromFlags("invalid", "value")
	if err == nil {
		t.Error("expected error for unknown condition type")
	}

	if !strings.Contains(err.Error(), "unknown stop condition type") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestExecuteStream_Success(t *testing.T) {
	chunks := []string{
		"data: chunk1\n",
		"data: chunk2\n",
		"data: chunk3\n",
		"data: [DONE]\n",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type: application/json, got %s", ct)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for _, chunk := range chunks {
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	condition := &stream.ContentMatchCondition{Pattern: "data: [DONE]"}
	metrics := &testMetrics{}

	client := &http.Client{Timeout: 5 * time.Second}
	result := stream.ExecuteStream(
		context.Background(),
		server.URL,
		`{"test": true}`,
		client,
		condition,
		metrics,
	)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	if result.ChunkCount != 4 {
		t.Errorf("expected 4 chunks, got %d", result.ChunkCount)
	}

	expectedBytes := 0
	for _, chunk := range chunks {
		expectedBytes += len(chunk)
	}

	if result.TotalBytes != expectedBytes {
		t.Errorf("expected %d bytes, got %d", expectedBytes, result.TotalBytes)
	}

	if metrics.chunks.Load() != int64(result.ChunkCount) {
		t.Errorf("metrics chunks mismatch: expected %d, got %d", result.ChunkCount, metrics.chunks.Load())
	}

	if metrics.bytes.Load() != int64(result.TotalBytes) {
		t.Errorf("metrics bytes mismatch: expected %d, got %d", result.TotalBytes, metrics.bytes.Load())
	}

	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

func TestExecuteStream_ByteLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for i := 0; i < 100; i++ {
			fmt.Fprintf(w, "data: chunk%d\n", i)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	condition := &stream.ByteLimitCondition{Limit: 50}
	client := &http.Client{Timeout: 5 * time.Second}

	result := stream.ExecuteStream(
		context.Background(),
		server.URL,
		`{}`,
		client,
		condition,
		nil,
	)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	if result.TotalBytes < 50 {
		t.Errorf("expected at least 50 bytes, got %d", result.TotalBytes)
	}

	if result.ChunkCount > 10 {
		t.Errorf("expected early termination, got %d chunks", result.ChunkCount)
	}
}

func TestExecuteStream_ChunkLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for i := 0; i < 100; i++ {
			fmt.Fprintf(w, "data: chunk%d\n", i)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	condition := &stream.ChunkLimitCondition{Limit: 5}
	client := &http.Client{Timeout: 5 * time.Second}

	result := stream.ExecuteStream(
		context.Background(),
		server.URL,
		`{}`,
		client,
		condition,
		nil,
	)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	if result.ChunkCount != 5 {
		t.Errorf("expected exactly 5 chunks, got %d", result.ChunkCount)
	}
}

func TestExecuteStream_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "Service temporarily unavailable")
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	result := stream.ExecuteStream(
		context.Background(),
		server.URL,
		`{}`,
		client,
		nil,
		nil,
	)

	if result.Err == nil {
		t.Fatal("expected error for 503 response")
	}

	if !strings.Contains(result.Err.Error(), "503") {
		t.Errorf("expected error to mention 503, got: %v", result.Err)
	}

	if result.ChunkCount != 0 {
		t.Errorf("expected 0 chunks for failed request, got %d", result.ChunkCount)
	}
}

func TestExecuteStream_ContextCancel(t *testing.T) {
	blockChan := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprint(w, "data: chunk1\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		<-blockChan
	}))
	defer server.Close()
	defer close(blockChan)

	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Timeout: 30 * time.Second}

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		time.Sleep(50 * time.Millisecond)
		blockChan <- struct{}{}
	}()

	result := stream.ExecuteStream(
		ctx,
		server.URL,
		`{}`,
		client,
		nil,
		nil,
	)

	if result.Err == nil {
		t.Fatal("expected context cancellation error")
	}

	errMsg := result.Err.Error()
	if !strings.Contains(errMsg, "context canceled") && !strings.Contains(errMsg, "context cancelled") {
		t.Errorf("expected error to mention context cancellation, got: %v", result.Err)
	}

	if result.ChunkCount == 0 {
		t.Error("expected at least 1 chunk before cancellation")
	}
}

func TestExecuteStreamWithObserver_Success(t *testing.T) {
	chunks := []string{
		"data: first\n",
		"data: second\n",
		"data: [DONE]\n",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for _, chunk := range chunks {
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	condition := &stream.ContentMatchCondition{Pattern: "data: [DONE]"}
	metrics := &testMetrics{}
	observer := &testObserver{}

	client := &http.Client{Timeout: 5 * time.Second}
	result := stream.ExecuteStreamWithObserver(
		context.Background(),
		server.URL,
		`{}`,
		client,
		condition,
		metrics,
		observer,
	)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	if observer.chunkCalls != 3 {
		t.Errorf("expected 3 chunk callbacks, got %d", observer.chunkCalls)
	}

	if observer.completeCalls != 1 {
		t.Errorf("expected 1 complete callback, got %d", observer.completeCalls)
	}

	if string(observer.lastChunk) != "data: [DONE]\n" {
		t.Errorf("expected last chunk to be 'data: [DONE]\\n', got %q", observer.lastChunk)
	}

	if observer.lastResult.ChunkCount != 3 {
		t.Errorf("expected result chunk count 3, got %d", observer.lastResult.ChunkCount)
	}

	if metrics.chunks.Load() != 3 {
		t.Errorf("expected metrics chunks 3, got %d", metrics.chunks.Load())
	}
}

func TestExecuteStreamWithObserver_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	observer := &testObserver{}
	client := &http.Client{Timeout: 5 * time.Second}

	result := stream.ExecuteStreamWithObserver(
		context.Background(),
		server.URL,
		`{}`,
		client,
		nil,
		nil,
		observer,
	)

	if result.Err == nil {
		t.Fatal("expected error for 400 response")
	}

	if observer.chunkCalls != 0 {
		t.Errorf("expected 0 chunk callbacks for failed request, got %d", observer.chunkCalls)
	}

	if observer.completeCalls != 1 {
		t.Errorf("expected 1 complete callback even on error, got %d", observer.completeCalls)
	}

	if observer.lastResult.Err == nil {
		t.Error("expected error in observer result")
	}
}

func TestExecuteStream_NoCondition(t *testing.T) {
	chunks := []string{
		"data: chunk1\n",
		"data: chunk2\n",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for _, chunk := range chunks {
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	result := stream.ExecuteStream(
		context.Background(),
		server.URL,
		`{}`,
		client,
		nil,
		nil,
	)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	if result.ChunkCount != 2 {
		t.Errorf("expected 2 chunks, got %d", result.ChunkCount)
	}
}

func TestExecuteStream_NoMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: test\n")
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	result := stream.ExecuteStream(
		context.Background(),
		server.URL,
		`{}`,
		client,
		&stream.ContentMatchCondition{Pattern: "test"},
		nil,
	)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	if result.ChunkCount != 1 {
		t.Errorf("expected 1 chunk, got %d", result.ChunkCount)
	}
}

func TestExecuteStreamWithObserver_NoObserver(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: test\n")
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	result := stream.ExecuteStreamWithObserver(
		context.Background(),
		server.URL,
		`{}`,
		client,
		&stream.ContentMatchCondition{Pattern: "test"},
		nil,
		nil,
	)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	if result.ChunkCount != 1 {
		t.Errorf("expected 1 chunk, got %d", result.ChunkCount)
	}
}
