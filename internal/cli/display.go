package cli

import (
	"fmt"
	"sync"
	"time"
)

type WorkerState int

const (
	StatePending WorkerState = iota
	StateStreaming
	StateSuccess
	StateError
)

type WorkerStatus struct {
	WorkerID   int
	HitID      int
	State      WorkerState
	ChunkCount int
	TotalBytes int64
	Duration   time.Duration
	ErrorMsg   string
}

type AggregateStats struct {
	TotalRequests      int64
	SuccessfulRequests int64
	FailedRequests     int64
	TotalChunks        int64
	TotalBytes         int64
	Elapsed            time.Duration
}

type AnimatedDisplay struct {
	workers       map[string]*WorkerStatus
	mu            sync.Mutex
	lastUpdate    time.Time
	updateThrottle time.Duration
	startTime     time.Time
}

func NewAnimatedDisplay(updateThrottle time.Duration) *AnimatedDisplay {
	return &AnimatedDisplay{
		workers:        make(map[string]*WorkerStatus),
		updateThrottle: updateThrottle,
		startTime:      time.Now(),
		lastUpdate:     time.Now(),
	}
}

func (d *AnimatedDisplay) RegisterWorker(workerID, hitID int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := fmt.Sprintf("W%d-H%d", workerID, hitID)
	d.workers[key] = &WorkerStatus{
		WorkerID: workerID,
		HitID:    hitID,
		State:    StatePending,
	}
}

func (d *AnimatedDisplay) UpdateWorker(workerID, hitID int, state WorkerState, chunkCount int, totalBytes int64, duration time.Duration, errorMsg string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := fmt.Sprintf("W%d-H%d", workerID, hitID)
	if worker, exists := d.workers[key]; exists {
		worker.State = state
		worker.ChunkCount = chunkCount
		worker.TotalBytes = totalBytes
		worker.Duration = duration
		worker.ErrorMsg = errorMsg
	}
}

func (d *AnimatedDisplay) Render(stats AggregateStats) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if now.Sub(d.lastUpdate) < d.updateThrottle {
		return
	}
	d.lastUpdate = now

	fmt.Print("\033[H\033[2J")

	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
	fmt.Println("                    Stream Artillery - Live Dashboard                      ")
	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
	fmt.Println()

	maxWorkers := 0
	for _, w := range d.workers {
		if w.WorkerID > maxWorkers {
			maxWorkers = w.WorkerID
		}
	}

	for wID := 0; wID <= maxWorkers; wID++ {
		workerLine := fmt.Sprintf("Worker %2d: ", wID)
		hasHits := false

		for hID := 0; hID < 100; hID++ {
			key := fmt.Sprintf("W%d-H%d", wID, hID)
			if worker, exists := d.workers[key]; exists {
				hasHits = true
				workerLine += d.formatWorkerStatus(worker) + " "
			}
		}

		if hasHits {
			fmt.Println(workerLine)
		}
	}

	fmt.Println()
	fmt.Println("───────────────────────────────────────────────────────────────────────────")
	fmt.Println("                           Aggregate Statistics                            ")
	fmt.Println("───────────────────────────────────────────────────────────────────────────")
	fmt.Printf("Elapsed:        %.1fs\n", stats.Elapsed.Seconds())
	fmt.Printf("Total Requests: %d\n", stats.TotalRequests)
	fmt.Printf("Successful:     \033[32m%d\033[0m (%.1f%%)\n",
		stats.SuccessfulRequests,
		percentage(stats.SuccessfulRequests, stats.TotalRequests))
	fmt.Printf("Failed:         \033[31m%d\033[0m (%.1f%%)\n",
		stats.FailedRequests,
		percentage(stats.FailedRequests, stats.TotalRequests))
	fmt.Printf("Total Chunks:   %d\n", stats.TotalChunks)
	fmt.Printf("Total Bytes:    %s\n", formatBytes(stats.TotalBytes))
	if stats.SuccessfulRequests > 0 {
		fmt.Printf("Avg Chunks/Req: %.2f\n", float64(stats.TotalChunks)/float64(stats.SuccessfulRequests))
	}
	fmt.Printf("Requests/Sec:   %.2f\n", float64(stats.TotalRequests)/stats.Elapsed.Seconds())
	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
}

func (d *AnimatedDisplay) formatWorkerStatus(w *WorkerStatus) string {
	switch w.State {
	case StatePending:
		return "\033[90m⏳\033[0m"
	case StateStreaming:
		return fmt.Sprintf("\033[33m🔄%d\033[0m", w.ChunkCount)
	case StateSuccess:
		return fmt.Sprintf("\033[32m✓%d\033[0m", w.ChunkCount)
	case StateError:
		return "\033[31m✗\033[0m"
	default:
		return "?"
	}
}

func (d *AnimatedDisplay) RenderFinal(stats AggregateStats) {
	d.mu.Lock()
	defer d.mu.Unlock()

	fmt.Print("\033[H\033[2J")

	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
	fmt.Println("                    Stream Artillery - Final Report                        ")
	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
	fmt.Println()

	fmt.Printf("Duration:             %.2fs\n", stats.Elapsed.Seconds())
	fmt.Printf("Total Requests:       %d\n", stats.TotalRequests)
	fmt.Printf("Successful:           %d (%.1f%%)\n",
		stats.SuccessfulRequests,
		percentage(stats.SuccessfulRequests, stats.TotalRequests))
	fmt.Printf("Failed:               %d (%.1f%%)\n",
		stats.FailedRequests,
		percentage(stats.FailedRequests, stats.TotalRequests))
	fmt.Printf("Total Chunks:         %d\n", stats.TotalChunks)
	fmt.Printf("Total Bytes:          %s\n", formatBytes(stats.TotalBytes))
	if stats.SuccessfulRequests > 0 {
		fmt.Printf("Avg Chunks/Request:   %.2f\n",
			float64(stats.TotalChunks)/float64(stats.SuccessfulRequests))
	}
	fmt.Printf("Requests/Second:      %.2f\n",
		float64(stats.TotalRequests)/stats.Elapsed.Seconds())

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════════════════")
	fmt.Println("                           Per-Request Details                             ")
	fmt.Println("═══════════════════════════════════════════════════════════════════════════")

	maxWorkers := 0
	for _, w := range d.workers {
		if w.WorkerID > maxWorkers {
			maxWorkers = w.WorkerID
		}
	}

	for wID := 0; wID <= maxWorkers; wID++ {
		for hID := 0; hID < 100; hID++ {
			key := fmt.Sprintf("W%d-H%d", wID, hID)
			if worker, exists := d.workers[key]; exists {
				status := "✓"
				color := "\033[32m"
				if worker.State == StateError {
					status = "✗"
					color = "\033[31m"
				}
				fmt.Printf("%s%s W%d-H%d: %d chunks, %s, %.2fs",
					color, status, worker.WorkerID, worker.HitID,
					worker.ChunkCount, formatBytes(worker.TotalBytes),
					worker.Duration.Seconds())
				if worker.ErrorMsg != "" {
					fmt.Printf(" - %s", worker.ErrorMsg)
				}
				fmt.Println("\033[0m")
			}
		}
	}
}

func percentage(part, total int64) float64 {
	if total == 0 {
		return 0.0
	}
	return float64(part) / float64(total) * 100.0
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
