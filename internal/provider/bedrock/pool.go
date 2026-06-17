package bedrockprov

import (
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// PoolWrapper wraps a bedrock.ModelPool to implement the generic provider.Pool interface.
type PoolWrapper struct {
	pool *bedrock.ModelPool
}

// NewPool creates a new provider.Pool from a bedrock.ModelPool.
func NewPool(pool *bedrock.ModelPool) *PoolWrapper {
	if pool == nil {
		return nil
	}
	return &PoolWrapper{pool: pool}
}

func (p *PoolWrapper) Primary() provider.Provider {
	return New(p.pool.Primary())
}

func (p *PoolWrapper) Auxiliary() provider.Provider {
	return New(p.pool.Auxiliary())
}

func (p *PoolWrapper) Planning() provider.Provider {
	return New(p.pool.Planning())
}

func (p *PoolWrapper) Info(role string) provider.PoolModelInfo {
	info := p.pool.Info(bedrock.ModelRole(role))
	return provider.PoolModelInfo{
		DisplayName:   info.DisplayName,
		ModelID:       info.ModelID,
		ContextWindow: info.ContextWindow,
	}
}

func (p *PoolWrapper) EstimateCost(role string, input, output, cacheRead, cacheWrite int64) float64 {
	return p.pool.EstimateCost(bedrock.ModelRole(role), input, output, cacheRead, cacheWrite)
}

// BedrockPool returns the underlying bedrock.ModelPool, in case Bedrock-specific methods are needed.
func (p *PoolWrapper) BedrockPool() *bedrock.ModelPool {
	return p.pool
}
