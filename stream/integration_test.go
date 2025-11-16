package stream_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stream-artillery/stream"
)

type IntegrationMetrics struct {
	chunks atomic.Int64
	bytes  atomic.Int64
}

func (m *IntegrationMetrics) AddTotalChunks(n int64) {
	m.chunks.Add(n)
}

func (m *IntegrationMetrics) AddTotalBytes(n int64) {
	m.bytes.Add(n)
}

type IntegrationObserver struct {
	chunksSeen    int
	totalBytesSeen int64
	completed      bool
	mu             sync.Mutex
}

func (o *IntegrationObserver) OnChunk(chunk []byte, chunkCount int, totalBytes int64, elapsed time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.chunksSeen = chunkCount
	o.totalBytesSeen = totalBytes
}

func (o *IntegrationObserver) OnComplete(result stream.StreamResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.completed = true
}

func (o *IntegrationObserver) GetStats() (chunks int, bytes int64, completed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.chunksSeen, o.totalBytesSeen, o.completed
}

func TestIntegration_RealWorldSSEStream(t *testing.T) {
	chunks := []string{
		"data: {\"id\":\"1\",\"content\":\"Hello\"}\n",
		"data: {\"id\":\"2\",\"content\":\" world\"}\n",
		"data: {\"id\":\"3\",\"content\":\"!\"}\n",
		"data: [DONE]\n",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		for _, chunk := range chunks {
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	metrics := &IntegrationMetrics{}
	observer := &IntegrationObserver{}

	condition := &stream.ContentMatchCondition{Pattern: "data: [DONE]"}

	client := &http.Client{Timeout: 5 * time.Second}

	result := stream.ExecuteStreamWithObserver(
		ctx,
		server.URL,
		`{"test": true}`,
		client,
		condition,
		metrics,
		observer,
	)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	if result.ChunkCount != 4 {
		t.Errorf("expected 4 chunks, got %d", result.ChunkCount)
	}

	if metrics.chunks.Load() != 4 {
		t.Errorf("metrics expected 4 chunks, got %d", metrics.chunks.Load())
	}

	obsChunks, obsBytes, obsCompleted := observer.GetStats()
	if obsChunks != 4 {
		t.Errorf("observer expected 4 chunks, got %d", obsChunks)
	}

	if obsBytes != int64(result.TotalBytes) {
		t.Errorf("observer bytes mismatch: expected %d, got %d", result.TotalBytes, obsBytes)
	}

	if !obsCompleted {
		t.Error("observer should have received completion callback")
	}
}

func TestIntegration_ConcurrentStreams(t *testing.T) {
	requestCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for i := 0; i < 5; i++ {
			fmt.Fprintf(w, "data: chunk-%d\n", i)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	ctx := context.Background()
	metrics := &IntegrationMetrics{}
	condition := &stream.ChunkLimitCondition{Limit: 5}

	client := &http.Client{Timeout: 5 * time.Second}

	concurrency := 10
	var wg sync.WaitGroup
	results := make([]stream.StreamResult, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			result := stream.ExecuteStream(
				ctx,
				server.URL,
				`{}`,
				client,
				condition,
				metrics,
			)

			results[idx] = result
		}(i)
	}

	wg.Wait()

	successCount := 0
	for _, result := range results {
		if result.Err == nil && result.ChunkCount == 5 {
			successCount++
		}
	}

	if successCount != concurrency {
		t.Errorf("expected %d successful requests, got %d", concurrency, successCount)
	}

	if requestCount.Load() != int32(concurrency) {
		t.Errorf("expected %d server requests, got %d", concurrency, requestCount.Load())
	}

	if metrics.chunks.Load() != int64(concurrency*5) {
		t.Errorf("expected %d total chunks, got %d", concurrency*5, metrics.chunks.Load())
	}
}

func TestIntegration_DynamicStopCondition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for i := 0; i < 100; i++ {
			fmt.Fprintf(w, "data: message-%d\n", i)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	ctx := context.Background()
	client := &http.Client{Timeout: 5 * time.Second}

	tests := []struct {
		name          string
		condType      string
		condValue     string
		expectChunks  int
		expectMinimum bool
	}{
		{
			name:          "byte_limit_500",
			condType:      "bytes",
			condValue:     "500",
			expectChunks:  30,
			expectMinimum: true,
		},
		{
			name:          "chunk_limit_10",
			condType:      "chunks",
			condValue:     "10",
			expectChunks:  10,
			expectMinimum: false,
		},
		{
			name:          "content_match",
			condType:      "content",
			condValue:     "message-50",
			expectChunks:  51,
			expectMinimum: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			condition, err := stream.NewStopConditionFromFlags(tt.condType, tt.condValue)
			if err != nil {
				t.Fatalf("failed to create condition: %v", err)
			}

			result := stream.ExecuteStream(
				ctx,
				server.URL,
				`{}`,
				client,
				condition,
				nil,
			)

			if result.Err != nil {
				t.Fatalf("unexpected error: %v", result.Err)
			}

			if tt.expectMinimum {
				if result.ChunkCount < tt.expectChunks {
					t.Errorf("expected at least %d chunks, got %d", tt.expectChunks, result.ChunkCount)
				}
			} else {
				if result.ChunkCount != tt.expectChunks {
					t.Errorf("expected exactly %d chunks, got %d", tt.expectChunks, result.ChunkCount)
				}
			}
		})
	}
}

func TestIntegration_ErrorRecovery(t *testing.T) {
	attemptCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attemptCount.Add(1)

		if attempt <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "Service temporarily unavailable")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: success\n")
	}))
	defer server.Close()

	ctx := context.Background()
	client := &http.Client{Timeout: 5 * time.Second}

	for i := 0; i < 3; i++ {
		result := stream.ExecuteStream(
			ctx,
			server.URL,
			`{}`,
			client,
			nil,
			nil,
		)

		if i < 2 {
			if result.Err == nil {
				t.Errorf("attempt %d: expected error, got success", i+1)
			}
		} else {
			if result.Err != nil {
				t.Errorf("attempt %d: expected success, got error: %v", i+1, result.Err)
			}
			if result.ChunkCount != 1 {
				t.Errorf("attempt %d: expected 1 chunk, got %d", i+1, result.ChunkCount)
			}
		}
	}
}

func TestIntegration_ContextTimeout(t *testing.T) {
	blockChan := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprint(w, "data: first\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		<-blockChan
	}))
	defer server.Close()
	defer close(blockChan)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	client := &http.Client{Timeout: 30 * time.Second}

	result := stream.ExecuteStream(
		ctx,
		server.URL,
		`{}`,
		client,
		nil,
		nil,
	)

	if result.Err == nil {
		t.Fatal("expected timeout error")
	}

	if result.ChunkCount == 0 {
		t.Error("expected at least 1 chunk before timeout")
	}

	if result.Duration < 100*time.Millisecond {
		t.Errorf("expected duration >= 100ms, got %v", result.Duration)
	}
}

func TestIntegration_ObserverProgressTracking(t *testing.T) {
	chunkData := []string{
		"data: chunk1\n",
		"data: chunk2\n",
		"data: chunk3\n",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for _, chunk := range chunkData {
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	observer := &IntegrationObserver{}

	client := &http.Client{Timeout: 5 * time.Second}

	result := stream.ExecuteStreamWithObserver(
		ctx,
		server.URL,
		`{}`,
		client,
		nil,
		nil,
		observer,
	)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	chunks, bytes, completed := observer.GetStats()

	if chunks != 3 {
		t.Errorf("observer expected 3 chunks, got %d", chunks)
	}

	if bytes == 0 {
		t.Error("observer expected non-zero bytes")
	}

	if !completed {
		t.Error("observer should have received completion callback")
	}

	if result.Duration < 60*time.Millisecond {
		t.Errorf("expected duration >= 60ms (3 chunks × 20ms), got %v", result.Duration)
	}
}
