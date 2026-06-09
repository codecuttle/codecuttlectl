package bedrock

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestPingCacheSkipsEmptyHistory(t *testing.T) {
	// PingCache should return nil (no-op) when there's no message history.
	// We can't construct a real client without AWS credentials, but we can
	// verify the early-return path by passing nil messages.
	c := &Client{modelID: "test-model", region: "us-east-1"}
	err := c.PingCache(context.Background(), "system", nil, nil)
	if err != nil {
		t.Errorf("PingCache with empty history should no-op, got: %v", err)
	}

	err = c.PingCache(context.Background(), "system", []types.Message{}, nil)
	if err != nil {
		t.Errorf("PingCache with zero-length history should no-op, got: %v", err)
	}
}
