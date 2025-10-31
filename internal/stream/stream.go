package stream

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type MetricsRecorder interface {
	AddTotalChunks(int64)
	AddTotalBytes(int64)
}

type StreamResult struct {
	ChunkCount int
	TotalBytes int
	Duration   time.Duration
	Err        error
}

func ExecuteStream(
	ctx context.Context,
	url string,
	requestBody string,
	client *http.Client,
	condition StreamStopCondition,
	metrics MetricsRecorder,
) StreamResult {
	result := StreamResult{}
	startTime := time.Now()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(requestBody))
	if err != nil {
		result.Err = fmt.Errorf("failed to create request: %w", err)
		return result
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream; charset=utf-8")
	req.Header.Set("Connection", "keep-alive")

	resp, err := client.Do(req)
	if err != nil {
		result.Err = fmt.Errorf("failed to send request: %w", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		result.Err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
		return result
	}

	reader := bufio.NewReader(resp.Body)

	for {
		select {
		case <-ctx.Done():
			result.Err = ctx.Err()
			result.Duration = time.Since(startTime)
			return result
		default:
		}

		chunk, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			result.Err = fmt.Errorf("error reading stream: %w", err)
			result.Duration = time.Since(startTime)
			return result
		}

		if len(chunk) > 0 {
			result.ChunkCount++
			result.TotalBytes += len(chunk)

			if metrics != nil {
				metrics.AddTotalChunks(1)
				metrics.AddTotalBytes(int64(len(chunk)))
			}

			if condition != nil && condition.ShouldStop(chunk, int64(result.TotalBytes), result.ChunkCount) {
				break
			}
		}
	}

	result.Duration = time.Since(startTime)
	return result
}
