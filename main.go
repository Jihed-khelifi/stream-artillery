package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

func main() {
	var url string
	var hits int
	var concurrency int
	var hcc int
	var jsonBody string

	flag.StringVar(&url, "url", "http://localhost:4000", "target URL")
	flag.IntVar(&concurrency, "w", 3, "number of concurrent workers")
	flag.IntVar(&hits, "hits", 1, "number of requests each worker sends simultaneously")
	flag.IntVar(&hcc, "hcc", 0, "number of http clients to create") // 0 means new client per request, otherwise share clients between workers
	flag.StringVar(&jsonBody, "jsonBody", "", "JSON body as string or path to .json file (if empty, uses default)")
	flag.Parse()

	if url == "" {
		log.Fatal("URL is required")
	}

	// Load JSON body from string or file
	requestBody, err := loadJSONBody(jsonBody)
	if err != nil {
		log.Fatalf("Failed to load JSON body: %v", err)
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
			runWorker(ctx, workerID, url, hits, stats, clients, requestBody)
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

func runWorker(ctx context.Context, workerID int, url string, hits int, stats *Stats, clients []*http.Client, requestBody string) {
	var wg sync.WaitGroup

	// Launch all hits simultaneously for this worker
	// NO SLEEP - we want concurrent requests on the same connection
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
				// Use round-robin to distribute requests across available clients
				clientIndex := (workerID*hits + hitID) % len(clients)
				client = clients[clientIndex]
			}

			makeStreamingRequest(ctx, workerID, hitID, url, stats, client, requestBody)
		}(i)
	}

	wg.Wait()
}

func makeStreamingRequest(ctx context.Context, workerID, hitID int, url string, stats *Stats, client *http.Client, requestBody string) {
	requestID := fmt.Sprintf("W%d-H%d", workerID, hitID)
	stats.totalRequests.Add(1)

	// isHighReasoning := (workerID+hitID)%2 == 0
	// body := defaultJSONBody
	// if isHighReasoning {
	// 	body = highReasoning
	// }
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(requestBody))
	if err != nil {
		logError(stats, requestID, "Failed to create request", err)
		stats.failedRequests.Add(1)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream; charset=utf-8")
	req.Header.Set("Connection", "keep-alive")

	startTime := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		logError(stats, requestID, "Failed to send request", err)
		stats.failedRequests.Add(1)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logError(stats, requestID, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status), nil)

		if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests {
			stats.maxConcurrentErrors.Add(1)
		}

		stats.failedRequests.Add(1)
		return
	}

	reader := bufio.NewReader(resp.Body)
	chunkCount := 0
	totalBytes := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		chunk, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				// End of stream reached successfully
				break
			}
			logError(stats, requestID, "Error reading stream", err)
			stats.failedRequests.Add(1)
			return
		}

		if len(chunk) > 0 {
			chunkCount++
			totalBytes += len(chunk)
			stats.totalChunks.Add(1)
			stats.totalBytes.Add(int64(len(chunk)))

			if bytes.Contains(chunk, []byte("data: [DONE]")) {
				break
			}
		}
	}

	duration := time.Since(startTime)
	stats.successfulRequests.Add(1)

	fmt.Printf("✓ %s completed: %d chunks, %d bytes, %.2fs\n",
		requestID, chunkCount, totalBytes, duration.Seconds())
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
