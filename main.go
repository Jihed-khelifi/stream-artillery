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

	"stream-artillery/internal/cli"
	"stream-artillery/stream"
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

	flag.StringVar(&url, "url", "http://localhost:4000", "Target SSE endpoint URL to stress test")
	flag.IntVar(&concurrency, "w", 3, "Number of concurrent workers (visualized as rows in the dashboard)")
	flag.IntVar(&hits, "hits", 1, "Number of simultaneous requests per worker (columns in each row)")
	flag.IntVar(&hcc, "hcc", 0, "HTTP client pool size: 0 = fresh client per request; >0 = reuse pooled clients")
	flag.StringVar(&jsonBody, "jsonBody", "", "Request body: JSON string, path to .json file, or empty for default OpenAI-style payload")
	flag.StringVar(&stopConditionType, "stop-condition-type", "content", "Stream stop condition: 'content' (match pattern), 'bytes' (byte limit), or 'chunks' (chunk limit)")
	flag.StringVar(&stopConditionValue, "stop-condition-value", "data: [DONE]", "Stop condition value: pattern string for 'content', numeric limit for 'bytes'/'chunks'")
	flag.Parse()

	if url == "" {
		log.Fatal("Error: -url flag is required")
	}

	if concurrency < 1 {
		log.Fatal("Error: -w (workers) must be at least 1")
	}

	if hits < 1 {
		log.Fatal("Error: -hits must be at least 1")
	}

	if hcc < 0 {
		log.Fatal("Error: -hcc (HTTP client count) cannot be negative")
	}

	totalRequests := concurrency * hits
	if totalRequests > 10000 {
		fmt.Printf("Warning: Planning to send %d total requests. This may be resource-intensive.\n", totalRequests)
		fmt.Print("Continue? (y/N): ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(strings.TrimSpace(response)) != "y" {
			fmt.Println("Aborted.")
			os.Exit(0)
		}
	}

	requestBody, err := loadJSONBody(jsonBody)
	if err != nil {
		log.Fatalf("Failed to load JSON body: %v", err)
	}

	stopCondition, err := stream.NewStopConditionFromFlags(stopConditionType, stopConditionValue)
	if err != nil {
		log.Fatalf("Failed to create stop condition: %v", err)
	}

	bodyDesc := "default OpenAI-style payload"
	if jsonBody != "" {
		if strings.HasSuffix(jsonBody, ".json") {
			bodyDesc = fmt.Sprintf("loaded from %s", jsonBody)
		} else {
			bodyDesc = "custom JSON string"
		}
	}

	fmt.Print("\033[H\033[2J")
	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
	fmt.Println("                         Stream Artillery v1.0                             ")
	fmt.Println("            SSE Streaming Stress Test with Live Visualization              ")
	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
	fmt.Printf("Target URL:        %s\n", url)
	fmt.Printf("Concurrency:       %d workers × %d hits = %d total requests\n", concurrency, hits, totalRequests)

	clientPoolDesc := "per-request"
	if hcc > 0 {
		clientPoolDesc = fmt.Sprintf("pooled (%d clients)", hcc)
	}
	fmt.Printf("HTTP Clients:      %s\n", clientPoolDesc)
	fmt.Printf("Stop Condition:    %s = '%s'\n", stopConditionType, stopConditionValue)
	fmt.Printf("Request Body:      %s\n", bodyDesc)
	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("Initializing workers and display...")
	time.Sleep(1 * time.Second)

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

	display := cli.NewAnimatedDisplay(100 * time.Millisecond)

	for i := 0; i < concurrency; i++ {
		for j := 0; j < hits; j++ {
			display.RegisterWorker(i, j)
		}
	}

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan bool)
	go renderLoop(display, stats, done)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runWorker(ctx, workerID, url, hits, stats, clients, requestBody, stopCondition, display)
		}(i)
	}

	wg.Wait()
	done <- true

	finalStats := cli.AggregateStats{
		TotalRequests:      stats.totalRequests.Load(),
		SuccessfulRequests: stats.successfulRequests.Load(),
		FailedRequests:     stats.failedRequests.Load(),
		TotalChunks:        stats.totalChunks.Load(),
		TotalBytes:         stats.totalBytes.Load(),
		Elapsed:            time.Since(stats.startTime),
	}

	display.RenderFinal(finalStats)
}

func runWorker(
	ctx context.Context,
	workerID int,
	url string,
	hits int,
	stats *Stats,
	clients []*http.Client,
	requestBody string,
	condition stream.StreamStopCondition,
	display *cli.AnimatedDisplay,
) {
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

			makeStreamingRequest(ctx, workerID, hitID, url, stats, client, requestBody, condition, display)
		}(i)
	}

	wg.Wait()
}

func makeStreamingRequest(
	ctx context.Context,
	workerID, hitID int,
	url string,
	stats *Stats,
	client *http.Client,
	requestBody string,
	condition stream.StreamStopCondition,
	display *cli.AnimatedDisplay,
) {
	requestID := fmt.Sprintf("W%d-H%d", workerID, hitID)
	stats.totalRequests.Add(1)

	observer := &streamObserver{
		workerID:  workerID,
		hitID:     hitID,
		display:   display,
		startTime: time.Now(),
	}

	display.UpdateWorker(workerID, hitID, cli.StateStreaming, 0, 0, 0, "")

	result := stream.ExecuteStreamWithObserver(ctx, url, requestBody, client, condition, stats, observer)

	if result.Err != nil {
		logError(stats, requestID, "Request failed", result.Err)

		if strings.Contains(result.Err.Error(), "503") || strings.Contains(result.Err.Error(), "429") {
			stats.maxConcurrentErrors.Add(1)
		}

		stats.failedRequests.Add(1)
		display.UpdateWorker(workerID, hitID, cli.StateError, result.ChunkCount, int64(result.TotalBytes), result.Duration, result.Err.Error())
		return
	}

	stats.successfulRequests.Add(1)
	display.UpdateWorker(workerID, hitID, cli.StateSuccess, result.ChunkCount, int64(result.TotalBytes), result.Duration, "")
}

type streamObserver struct {
	workerID  int
	hitID     int
	display   *cli.AnimatedDisplay
	startTime time.Time
}

func (o *streamObserver) OnChunk(chunk []byte, chunkCount int, totalBytes int64, elapsed time.Duration) {
	o.display.UpdateWorker(o.workerID, o.hitID, cli.StateStreaming, chunkCount, totalBytes, elapsed, "")
}

func (o *streamObserver) OnComplete(result stream.StreamResult) {
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

func renderLoop(display *cli.AnimatedDisplay, stats *Stats, done chan bool) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			aggregateStats := cli.AggregateStats{
				TotalRequests:      stats.totalRequests.Load(),
				SuccessfulRequests: stats.successfulRequests.Load(),
				FailedRequests:     stats.failedRequests.Load(),
				TotalChunks:        stats.totalChunks.Load(),
				TotalBytes:         stats.totalBytes.Load(),
				Elapsed:            time.Since(stats.startTime),
			}
			display.Render(aggregateStats)
		}
	}
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
