package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"stream-artillery/internal/stream"
)

const defaultJSONBody = `{
  "messages": [
    {
      "role": "user",
      "content": "Use the tool to look up Beans in Spring"
    }
  ],
  "model": "gpt-5-mini",
  "stream": true,
  "reasoning_effort": "high",
  "stream_options": {
    "include_usage": true
  },
  "service_tier": "auto",
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "spring_boot_docs",
        "description": "Get Spring documentation of specific concept in Spring",
        "parameters": {
          "type": "object",
          "properties": {
            "concept": {
              "type": "string",
              "description": "The Spring concept to search for"
            }
          },
          "strict": false
        }
      }
    }
  ]
}`

type Stats struct {
	totalRequests       atomic.Int64
	successfulRequests  atomic.Int64
	failedRequests      atomic.Int64
	totalChunks         atomic.Int64
	totalBytes          atomic.Int64
	maxConcurrentErrors atomic.Int64
	startTime           time.Time
	errorLogger         *log.Logger
	errorFile           *os.File
	mu                  sync.Mutex
}

func (s *Stats) AddTotalChunks(n int64) {
	s.totalChunks.Add(n)
}

func (s *Stats) AddTotalBytes(n int64) {
	s.totalBytes.Add(n)
}

func main() {
	var url string
	var hits int
	var concurrency int
	var hcc int
	var jsonBody string
	var stopConditionType string
	var stopConditionValue string

	flag.StringVar(&url, "url", "http://localhost:4000", "target URL")
	flag.IntVar(&concurrency, "w", 3, "number of concurrent workers")
	flag.IntVar(&hits, "hits", 1, "number of requests each worker sends simultaneously")
	flag.IntVar(&hcc, "hcc", 0, "number of http clients to create")
	flag.StringVar(&jsonBody, "jsonBody", "", "JSON body as string or path to .json file (if empty, uses default)")
	flag.StringVar(&stopConditionType, "stop-condition-type", "content", "stop condition type: content, bytes, or chunks")
	flag.StringVar(&stopConditionValue, "stop-condition-value", "data: [DONE]", "stop condition value (pattern for content, numeric limit for bytes/chunks)")
	flag.Parse()

	if url == "" {
		log.Fatal("URL is required")
	}

	requestBody, err := loadJSONBody(jsonBody)
	if err != nil {
		log.Fatalf("Failed to load JSON body: %v", err)
	}

	stopCondition, err := stream.NewStopConditionFromFlags(stopConditionType, stopConditionValue)
	if err != nil {
		log.Fatalf("Failed to create stop condition: %v", err)
	}

	fmt.Printf("--Stream Artillery - Stress Test for Brokk-llm-- \n")
	fmt.Printf("Target URL: %s\n", url)
	fmt.Printf("Workers: %d\n", concurrency)
	fmt.Printf("Hits per worker: %d\n", hits)
	fmt.Printf("Total requests: %d\n", concurrency*hits)
	fmt.Printf("HTTP Clients: %d\n", hcc)
	fmt.Printf("\n\n")

	errorFile, err := os.Create("errors.log")
	if err != nil {
		log.Fatal("Failed to create error log file:", err)
	}
	defer errorFile.Close()

	stats := &Stats{
		startTime:   time.Now(),
		errorLogger: log.New(errorFile, "", log.LstdFlags),
		errorFile:   errorFile,
	}

	stats.errorLogger.Println("=== Stream Artillery Error Log ===")
	stats.errorLogger.Printf("Target: %s, Workers: %d, Hits: %d, HTTP Clients: %d\n", url, concurrency, hits, hcc)

	var clients []*http.Client
	if hcc == 0 {
		clients = nil
	} else {
		clients = make([]*http.Client, hcc)
		for i := 0; i < hcc; i++ {
			clients[i] = &http.Client{
				Timeout: 5 * time.Minute,
				Transport: &http.Transport{
					MaxIdleConns:        100,
					MaxIdleConnsPerHost: 100,
					IdleConnTimeout:     90 * time.Second,
				},
			}
		}
	}

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start workers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runWorker(ctx, workerID, url, hits, stats, clients, requestBody, stopCondition)
		}(i)
	}

	done := make(chan bool)
	go reportStats(stats, done)

	wg.Wait()
	done <- true

	duration := time.Since(stats.startTime)
	total := stats.totalRequests.Load()
	successful := stats.successfulRequests.Load()
	failed := stats.failedRequests.Load()
	chunks := stats.totalChunks.Load()
	bytes := stats.totalBytes.Load()
	maxConcurrentErrors := stats.maxConcurrentErrors.Load()

	fmt.Printf("\n\n")
	fmt.Printf("Duration:             %.2fs\n", duration.Seconds())
	fmt.Printf("Total Requests:       %d\n", total)
	fmt.Printf("Successful:           %d (%.1f%%)\n", successful, float64(successful)/float64(total)*100)
	fmt.Printf("Failed:               %d (%.1f%%)\n", failed, float64(failed)/float64(total)*100)
	fmt.Printf("Max Concurrent Errors: %d\n", maxConcurrentErrors)
	fmt.Printf("Total Chunks:         %d\n", chunks)
	fmt.Printf("Total Bytes:          %s\n", formatBytes(bytes))
	fmt.Printf("Avg Chunks/Request:   %.2f\n", float64(chunks)/float64(successful))
	fmt.Printf("Requests/Second:      %.2f\n", float64(total)/duration.Seconds())

}

func runWorker(ctx context.Context, workerID int, url string, hits int, stats *Stats, clients []*http.Client, requestBody string, condition stream.StreamStopCondition) {
	var wg sync.WaitGroup

	for i := 0; i < hits; i++ {
		wg.Add(1)
		go func(hitID int) {
			defer wg.Done()

			var client *http.Client
			if clients == nil {
				client = &http.Client{
					Timeout: 5 * time.Minute,
					Transport: &http.Transport{
						MaxIdleConns:        100,
						MaxIdleConnsPerHost: 100,
						IdleConnTimeout:     90 * time.Second,
					},
				}
			} else {
				clientIndex := (workerID*hits + hitID) % len(clients)
				client = clients[clientIndex]
			}

			makeStreamingRequest(ctx, workerID, hitID, url, stats, client, requestBody, condition)
		}(i)
	}

	wg.Wait()
}

func makeStreamingRequest(ctx context.Context, workerID, hitID int, url string, stats *Stats, client *http.Client, requestBody string, condition stream.StreamStopCondition) {
	requestID := fmt.Sprintf("W%d-H%d", workerID, hitID)
	stats.totalRequests.Add(1)

	result := stream.ExecuteStream(ctx, url, requestBody, client, condition, stats)

	if result.Err != nil {
		logError(stats, requestID, "Request failed", result.Err)

		if strings.Contains(result.Err.Error(), "503") || strings.Contains(result.Err.Error(), "429") {
			stats.maxConcurrentErrors.Add(1)
		}

		stats.failedRequests.Add(1)
		return
	}

	stats.successfulRequests.Add(1)
	fmt.Printf("✓ %s completed: %d chunks, %d bytes, %.2fs\n",
		requestID, result.ChunkCount, result.TotalBytes, result.Duration.Seconds())
}

func logError(stats *Stats, requestID, message string, err error) {
	stats.mu.Lock()
	defer stats.mu.Unlock()

	if err != nil {
		errMsg := fmt.Sprintf("[%s] %s: %v", requestID, message, err)
		stats.errorLogger.Println(errMsg)
		fmt.Printf("✗ %s\n", errMsg)
	} else {
		errMsg := fmt.Sprintf("[%s] %s", requestID, message)
		stats.errorLogger.Println(errMsg)
		fmt.Printf("✗ %s\n", errMsg)
	}
}

func reportStats(stats *Stats, done chan bool) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			ticker.Stop()
			return
		case <-ticker.C:
			elapsed := time.Since(stats.startTime).Seconds()
			total := stats.totalRequests.Load()
			successful := stats.successfulRequests.Load()
			failed := stats.failedRequests.Load()
			chunks := stats.totalChunks.Load()
			bytes := stats.totalBytes.Load()

			fmt.Printf("\n📊 Stats [%.1fs]: Total: %d | Success: %d | Failed: %d | Chunks: %d | Bytes: %s | RPS: %.2f\n\n",
				elapsed, total, successful, failed, chunks, formatBytes(bytes), float64(total)/elapsed)
		}
	}
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func loadJSONBody(input string) (string, error) {
	if input == "" {
		return defaultJSONBody, nil
	}

	input = strings.TrimSpace(input)

	if strings.HasSuffix(input, ".json") {
		if strings.HasPrefix(input, "~") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home directory: %w", err)
			}
			input = filepath.Join(home, input[1:])
		}

		data, err := os.ReadFile(input)
		if err != nil {
			return "", fmt.Errorf("failed to read JSON file: %w", err)
		}

		var jsonData interface{}
		if err := json.Unmarshal(data, &jsonData); err != nil {
			return "", fmt.Errorf("invalid JSON in file: %w", err)
		}

		return string(data), nil
	}

	var jsonData interface{}
	if err := json.Unmarshal([]byte(input), &jsonData); err != nil {
		return "", fmt.Errorf("invalid JSON string: %w", err)
	}

	return input, nil
}
