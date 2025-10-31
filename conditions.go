package main

import (
	"bytes"
	"fmt"
	"strconv"
)

type StreamStopCondition interface {
	ShouldStop(chunk []byte, totalBytes int64, chunkCount int) bool
}

type ContentMatchCondition struct {
	Pattern string
}

func (c *ContentMatchCondition) ShouldStop(chunk []byte, totalBytes int64, chunkCount int) bool {
	return bytes.Contains(chunk, []byte(c.Pattern))
}

type ByteLimitCondition struct {
	Limit int64
}

func (c *ByteLimitCondition) ShouldStop(chunk []byte, totalBytes int64, chunkCount int) bool {
	return totalBytes >= c.Limit
}

type ChunkLimitCondition struct {
	Limit int
}

func (c *ChunkLimitCondition) ShouldStop(chunk []byte, totalBytes int64, chunkCount int) bool {
	return chunkCount >= c.Limit
}

func NewStopConditionFromFlags(conditionType string, conditionValue string) (StreamStopCondition, error) {
	switch conditionType {
	case "content":
		if conditionValue == "" {
			return nil, fmt.Errorf("content match condition requires a pattern value")
		}
		return &ContentMatchCondition{Pattern: conditionValue}, nil

	case "bytes":
		if conditionValue == "" {
			return nil, fmt.Errorf("byte limit condition requires a numeric value")
		}
		limit, err := strconv.ParseInt(conditionValue, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid byte limit value: %w", err)
		}
		if limit <= 0 {
			return nil, fmt.Errorf("byte limit must be positive")
		}
		return &ByteLimitCondition{Limit: limit}, nil

	case "chunks":
		if conditionValue == "" {
			return nil, fmt.Errorf("chunk limit condition requires a numeric value")
		}
		limit, err := strconv.Atoi(conditionValue)
		if err != nil {
			return nil, fmt.Errorf("invalid chunk limit value: %w", err)
		}
		if limit <= 0 {
			return nil, fmt.Errorf("chunk limit must be positive")
		}
		return &ChunkLimitCondition{Limit: limit}, nil

	default:
		return nil, fmt.Errorf("unknown stop condition type: %s (valid types: content, bytes, chunks)", conditionType)
	}
}
