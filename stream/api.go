// Package stream provides helpers for making streaming HTTP requests to SSE-style
// endpoints and consuming newline-delimited chunks with flexible stop conditions,
// optional metrics recording, and per-stream progress observers.
package stream

import (
	"context"
	"net/http"

	internalstream "stream-artillery/internal/stream"
)

// MetricsRecorder records aggregate counts of chunks and bytes across one or more streams.
type MetricsRecorder = internalstream.MetricsRecorder

// StreamObserver receives per-chunk and completion callbacks for visualization or logging.
type StreamObserver = internalstream.StreamObserver

// StreamResult holds summary information about a completed stream including chunk count,
// total bytes received, duration, and any error that occurred.
type StreamResult = internalstream.StreamResult

// StreamStopCondition controls when streaming stops by evaluating each chunk
// and the current stream state.
type StreamStopCondition = internalstream.StreamStopCondition

// ContentMatchCondition stops streaming when a chunk contains the specified pattern.
type ContentMatchCondition = internalstream.ContentMatchCondition

// ByteLimitCondition stops streaming when the total bytes received reaches the specified limit.
type ByteLimitCondition = internalstream.ByteLimitCondition

// ChunkLimitCondition stops streaming when the number of chunks received reaches the specified limit.
type ChunkLimitCondition = internalstream.ChunkLimitCondition

// ExecuteStream sends an HTTP POST request with a JSON body to a streaming endpoint
// and reads newline-delimited chunks until EOF, error, context cancellation, or an
// optional stop condition is met. Optionally records aggregate metrics via MetricsRecorder.
func ExecuteStream(
	ctx context.Context,
	url string,
	requestBody string,
	client *http.Client,
	condition StreamStopCondition,
	metrics MetricsRecorder,
) StreamResult {
	return internalstream.ExecuteStream(ctx, url, requestBody, client, condition, metrics)
}

// ExecuteStreamWithObserver works like ExecuteStream but also invokes the provided
// observer on each chunk received and on stream completion, enabling real-time
// progress tracking and visualization.
func ExecuteStreamWithObserver(
	ctx context.Context,
	url string,
	requestBody string,
	client *http.Client,
	condition StreamStopCondition,
	metrics MetricsRecorder,
	observer StreamObserver,
) StreamResult {
	return internalstream.ExecuteStreamWithObserver(ctx, url, requestBody, client, condition, metrics, observer)
}

// NewStopConditionFromFlags creates a StreamStopCondition from string parameters,
// useful for CLI flags or configuration files. Supported types are "content" (pattern match),
// "bytes" (byte limit), and "chunks" (chunk count limit).
func NewStopConditionFromFlags(conditionType, conditionValue string) (StreamStopCondition, error) {
	return internalstream.NewStopConditionFromFlags(conditionType, conditionValue)
}
