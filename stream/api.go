package stream

import (
	"context"
	"net/http"

	internalstream "stream-artillery/internal/stream"
)

type MetricsRecorder = internalstream.MetricsRecorder
type StreamResult = internalstream.StreamResult
type StreamStopCondition = internalstream.StreamStopCondition

type ContentMatchCondition = internalstream.ContentMatchCondition
type ByteLimitCondition = internalstream.ByteLimitCondition
type ChunkLimitCondition = internalstream.ChunkLimitCondition

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

func NewStopConditionFromFlags(conditionType, conditionValue string) (StreamStopCondition, error) {
	return internalstream.NewStopConditionFromFlags(conditionType, conditionValue)
}
