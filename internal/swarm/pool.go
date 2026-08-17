package swarm

import (
	"context"
	"fmt"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// ProviderFactory is a function that can create a provider given its name, model ID, and context.
// This allows breaking dependency cycles.
type ProviderFactory func(ctx context.Context, providerName, modelID string) (provider.Provider, error)

// Pool implements provider.Pool backed by a Swarm Morphology.
type Pool struct {
	morph     *Morphology
	providers map[string]provider.Provider
	infos     map[string]provider.PoolModelInfo

	// Legacy mapping
	primaryNodeID   string
	auxiliaryNodeID string
	planningNodeID  string
}

// NewPool initializes a new Swarm Pool by instantiating the providers for each node.
func NewPool(ctx context.Context, morph *Morphology, factory ProviderFactory) (*Pool, error) {
	p := &Pool{
		morph:     morph,
		providers: make(map[string]provider.Provider),
		infos:     make(map[string]provider.PoolModelInfo),
	}

	for nodeID, nodeConfig := range morph.Nodes {
		prov, err := factory(ctx, nodeConfig.Provider, nodeConfig.Model)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize node %q: %w", nodeID, err)
		}
		p.providers[nodeID] = prov

		cw := int32(0)
		if cwp, ok := prov.(provider.ContextWindowProvider); ok {
			cw = cwp.ContextWindow()
		}

		p.infos[nodeID] = provider.PoolModelInfo{
			DisplayName:   nodeConfig.Model, // or a cleaner alias
			ModelID:       nodeConfig.Model,
			ContextWindow: cw,
		}

		if nodeConfig.IsPrimary {
			p.primaryNodeID = nodeID
		}
	}

	// For legacy interface support, map auxiliary and planning if nodes with those names exist.
	// Otherwise, fallback to primary.
	p.auxiliaryNodeID = p.primaryNodeID
	if _, ok := p.providers["auxiliary"]; ok {
		p.auxiliaryNodeID = "auxiliary"
	}
	p.planningNodeID = p.primaryNodeID
	if _, ok := p.providers["planning"]; ok {
		p.planningNodeID = "planning"
	}

	return p, nil
}

func (p *Pool) Primary() provider.Provider {
	return p.providers[p.primaryNodeID]
}

func (p *Pool) Auxiliary() provider.Provider {
	return p.providers[p.auxiliaryNodeID]
}

func (p *Pool) Planning() provider.Provider {
	return p.providers[p.planningNodeID]
}

func (p *Pool) Info(role string) provider.PoolModelInfo {
	nodeID := p.roleToNodeID(role)
	return p.infos[nodeID]
}

func (p *Pool) EstimateCost(role string, input, output, cacheRead, cacheWrite int64) float64 {
	nodeID := p.roleToNodeID(role)
	prov := p.providers[nodeID]

	if ce, ok := prov.(provider.CostEstimator); ok {
		return ce.EstimateCost(provider.Usage{
			InputTokens:      int32(input),
			OutputTokens:     int32(output),
			CacheReadTokens:  int32(cacheRead),
			CacheWriteTokens: int32(cacheWrite),
		})
	}
	return 0
}

// GetNode returns the provider for a specific node ID in the swarm.
func (p *Pool) GetNode(nodeID string) (provider.Provider, bool) {
	prov, ok := p.providers[nodeID]
	return prov, ok
}

// roleToNodeID maps legacy role strings ("primary", "auxiliary", "planning") to node IDs.
func (p *Pool) roleToNodeID(role string) string {
	switch role {
	case "primary":
		return p.primaryNodeID
	case "auxiliary":
		return p.auxiliaryNodeID
	case "planning":
		return p.planningNodeID
	default:
		// If role matches a node ID directly
		if _, ok := p.providers[role]; ok {
			return role
		}
		return p.primaryNodeID
	}
}
