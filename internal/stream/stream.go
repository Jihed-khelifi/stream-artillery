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

type StreamObserver interface {
	OnChunk(chunk []byte, chunkCount int, totalBytes int64, elapsed time.Duration)
	OnComplete(result StreamResult)
}

type StreamResult struct {
	ChunkCount int
	TotalBytes int
	Duration   time.Duration
	Err        error
}

func ExecuteStreamWithObserver(
	ctx context.Context,
	url string,
	requestBody string,
	client *http.Client,
	condition StreamStopCondition,
	metrics MetricsRecorder,
	observer StreamObserver,
) StreamResult {
	result := StreamResult{}
	startTime := time.Now()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(requestBody))
	if err != nil {
		result.Err = fmt.Errorf("failed to create request: %w", err)
		result.Duration = time.Since(startTime)
		if observer != nil {
			observer.OnComplete(result)
		}
		return result
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream; charset=utf-8")
	req.Header.Set("Connection", "keep-alive")

	resp, err := client.Do(req)
	if err != nil {
		result.Err = fmt.Errorf("failed to send request: %w", err)
		result.Duration = time.Since(startTime)
		if observer != nil {
			observer.OnComplete(result)
		}
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		result.Err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
		result.Duration = time.Since(startTime)
		if observer != nil {
			observer.OnComplete(result)
		}
		return result
	}

	reader := bufio.NewReader(resp.Body)

	for {
		select {
		case <-ctx.Done():
			result.Err = ctx.Err()
			result.Duration = time.Since(startTime)
			if observer != nil {
				observer.OnComplete(result)
			}
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
			if observer != nil {
				observer.OnComplete(result)
			}
			return result
		}

		if len(chunk) > 0 {
			result.ChunkCount++
			result.TotalBytes += len(chunk)

			if metrics != nil {
				metrics.AddTotalChunks(1)
				metrics.AddTotalBytes(int64(len(chunk)))
			}

			if observer != nil {
				elapsed := time.Since(startTime)
				observer.OnChunk(chunk, result.ChunkCount, int64(result.TotalBytes), elapsed)
			}

			if condition != nil && condition.ShouldStop(chunk, int64(result.TotalBytes), result.ChunkCount) {
				break
			}
		}
	}

	result.Duration = time.Since(startTime)
	if observer != nil {
		observer.OnComplete(result)
	}
	return result
}

func ExecuteStream(
	ctx context.Context,
	url string,
	requestBody string,
	client *http.Client,
	condition StreamStopCondition,
	metrics MetricsRecorder,
) StreamResult {
	return ExecuteStreamWithObserver(ctx, url, requestBody, client, condition, metrics, nil)
}
