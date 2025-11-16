package main

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"stream-artillery/stream"
)

type SimpleMetrics struct {
	chunks atomic.Int64
	bytes  atomic.Int64
}

func (m *SimpleMetrics) AddTotalChunks(n int64) {
	m.chunks.Add(n)
}

func (m *SimpleMetrics) AddTotalBytes(n int64) {
	m.bytes.Add(n)
}

type ProgressObserver struct {
	requestID string
}

func (o *ProgressObserver) OnChunk(chunk []byte, chunkCount int, totalBytes int64, elapsed time.Duration) {
	fmt.Printf("[%s] Chunk %d: %d bytes (total: %d bytes, elapsed: %.2fs)\n",
		o.requestID, chunkCount, len(chunk), totalBytes, elapsed.Seconds())
}

func (o *ProgressObserver) OnComplete(result stream.StreamResult) {
	if result.Err != nil {
		fmt.Printf("[%s] ✗ Failed: %v\n", o.requestID, result.Err)
	} else {
		fmt.Printf("[%s] ✓ Complete: %d chunks, %d bytes in %.2fs\n",
			o.requestID, result.ChunkCount, result.TotalBytes, result.Duration.Seconds())
	}
}

func main() {
	fmt.Println("Stream Artillery - Library Usage Example")
	fmt.Println("=========================================")
	fmt.Println()

	targetURL := "http://localhost:4000/chat/completions"
	requestBody := `{
		"messages": [{"role": "user", "content": "Hello, world!"}],
		"model": "gpt-5",
		"stream": true
	}`

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	fmt.Println("Example 1: Content Match Stop Condition")
	fmt.Println("----------------------------------------")
	runExample1(targetURL, requestBody, client)

	fmt.Println("\nExample 2: Byte Limit Stop Condition")
	fmt.Println("-------------------------------------")
	runExample2(targetURL, requestBody, client)

	fmt.Println("\nExample 3: Chunk Limit Stop Condition")
	fmt.Println("--------------------------------------")
	runExample3(targetURL, requestBody, client)

	fmt.Println("\nExample 4: Multiple Concurrent Streams")
	fmt.Println("---------------------------------------")
	runExample4(targetURL, requestBody, client)
}

func runExample1(url, body string, client *http.Client) {
	ctx := context.Background()
	metrics := &SimpleMetrics{}

	condition := &stream.ContentMatchCondition{Pattern: "data: [DONE]"}

	observer := &ProgressObserver{requestID: "content-match"}

	result := stream.ExecuteStreamWithObserver(
		ctx, url, body, client, condition, metrics, observer,
	)

	fmt.Printf("\nFinal metrics: %d chunks, %d bytes\n",
		metrics.chunks.Load(), metrics.bytes.Load())

	if result.Err != nil {
		fmt.Printf("Note: This example requires a running SSE server at %s\n", url)
	}
}

func runExample2(url, body string, client *http.Client) {
	ctx := context.Background()
	metrics := &SimpleMetrics{}

	condition := &stream.ByteLimitCondition{Limit: 1024}

	observer := &ProgressObserver{requestID: "byte-limit"}

	result := stream.ExecuteStreamWithObserver(
		ctx, url, body, client, condition, metrics, observer,
	)

	fmt.Printf("\nStopped after %d bytes (limit: 1024)\n", result.TotalBytes)
}

func runExample3(url, body string, client *http.Client) {
	ctx := context.Background()
	metrics := &SimpleMetrics{}

	condition := &stream.ChunkLimitCondition{Limit: 5}

	observer := &ProgressObserver{requestID: "chunk-limit"}

	result := stream.ExecuteStreamWithObserver(
		ctx, url, body, client, condition, metrics, observer,
	)

	fmt.Printf("\nStopped after %d chunks (limit: 5)\n", result.ChunkCount)
}

func runExample4(url, body string, client *http.Client) {
	ctx := context.Background()
	metrics := &SimpleMetrics{}

	condition := &stream.ChunkLimitCondition{Limit: 3}

	type requestResult struct {
		id     int
		result stream.StreamResult
	}

	results := make(chan requestResult, 5)

	for i := 0; i < 5; i++ {
		go func(requestID int) {
			observer := &ProgressObserver{
				requestID: fmt.Sprintf("concurrent-%d", requestID),
			}

			result := stream.ExecuteStreamWithObserver(
				ctx, url, body, client, condition, metrics, observer,
			)

			results <- requestResult{id: requestID, result: result}
		}(i)
	}

	successCount := 0
	for i := 0; i < 5; i++ {
		res := <-results
		if res.result.Err == nil {
			successCount++
		}
	}

	fmt.Printf("\nCompleted %d/5 concurrent requests\n", successCount)
	fmt.Printf("Total metrics: %d chunks, %d bytes\n",
		metrics.chunks.Load(), metrics.bytes.Load())
}
