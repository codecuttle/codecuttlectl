package bedrock

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// PoolConfig specifies which models to initialize in the pool.
type PoolConfig struct {
	Region  string
	Profile string

	// Primary model — used for main agent conversation.
	Primary string

	// Auxiliary model — used for summaries, titles, classification.
	// If empty, auto-selected via DefaultRoles.
	Auxiliary string

	// Planning model — mid-tier for complex auxiliary tasks.
	// If empty, auto-selected via DefaultRoles.
	Planning string
}

// ModelPool holds pre-initialized clients for each configured model role.
type ModelPool struct {
	mu      sync.RWMutex
	clients map[ModelRole]*Client
	infos   map[ModelRole]ModelInfo
	region  string
}

// NewPool creates a ModelPool with clients for all three roles.
// If Auxiliary or Planning are empty in the config, they are auto-selected
// based on the primary model using DefaultRoles.
// Verifies model access on initialization — if auxiliary/planning models are
// not accessible, they silently fall back to the primary model.
func NewPool(ctx context.Context, cfg PoolConfig) (*ModelPool, error) {
	region := cfg.Region
	if region == "" {
		region = "us-west-2"
	}

	// Resolve aliases
	cfg.Primary = ResolveModelID(cfg.Primary)
	if cfg.Auxiliary != "" {
		cfg.Auxiliary = ResolveModelID(cfg.Auxiliary)
	}
	if cfg.Planning != "" {
		cfg.Planning = ResolveModelID(cfg.Planning)
	}

	// Auto-populate missing roles
	defaults := DefaultRoles(cfg.Primary)
	if cfg.Auxiliary == "" {
		cfg.Auxiliary = defaults.Auxiliary
	}
	if cfg.Planning == "" {
		cfg.Planning = defaults.Planning
	}

	// Load AWS config once, shared across all clients
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if cfg.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(cfg.Profile))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	pool := &ModelPool{
		clients: make(map[ModelRole]*Client),
		infos:   make(map[ModelRole]ModelInfo),
		region:  region,
	}

	// Initialize all three roles
	for role, modelID := range map[ModelRole]string{
		RolePrimary:   cfg.Primary,
		RoleAuxiliary: cfg.Auxiliary,
		RolePlanning:  cfg.Planning,
	} {
		runtime := bedrockruntime.NewFromConfig(awsCfg)
		pool.clients[role] = &Client{
			runtime: runtime,
			modelID: modelID,
			region:  region,
		}
		pool.infos[role] = LookupModel(modelID)
	}

	// Verify auxiliary/planning model access with a lightweight probe.
	// If access is denied, silently fall back to primary for that role.
	for _, role := range []ModelRole{RoleAuxiliary, RolePlanning} {
		if pool.infos[role].ModelID == pool.infos[RolePrimary].ModelID {
			continue // Same model, no need to probe
		}
		if err := pool.clients[role].probeAccess(ctx); err != nil {
			// Access denied or other error — fall back to primary
			pool.clients[role] = pool.clients[RolePrimary]
			pool.infos[role] = pool.infos[RolePrimary]
		}
	}

	return pool, nil
}

// Get returns the client for the given role.
// Falls back to Primary if the requested role is somehow missing.
func (p *ModelPool) Get(role ModelRole) *Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if c, ok := p.clients[role]; ok {
		return c
	}
	return p.clients[RolePrimary]
}

// Primary returns the primary model client (convenience shorthand).
func (p *ModelPool) Primary() *Client {
	return p.Get(RolePrimary)
}

// Auxiliary returns the auxiliary model client (convenience shorthand).
func (p *ModelPool) Auxiliary() *Client {
	return p.Get(RoleAuxiliary)
}

// Planning returns the planning model client (convenience shorthand).
func (p *ModelPool) Planning() *Client {
	return p.Get(RolePlanning)
}

// Info returns the ModelInfo for the given role.
func (p *ModelPool) Info(role ModelRole) ModelInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if info, ok := p.infos[role]; ok {
		return info
	}
	return p.infos[RolePrimary]
}

// Region returns the configured AWS region.
func (p *ModelPool) Region() string {
	return p.region
}

// DefaultRoles returns the standard role assignment for a given primary model.
// The system always initializes all three roles — no opt-in required.
func DefaultRoles(primaryID string) PoolConfig {
	switch {
	case strings.Contains(primaryID, "opus"):
		return PoolConfig{
			Primary:   primaryID,
			Auxiliary: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
			Planning:  "us.anthropic.claude-sonnet-4-6",
		}
	case strings.Contains(primaryID, "sonnet"):
		return PoolConfig{
			Primary:   primaryID,
			Auxiliary: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
			Planning:  primaryID, // Sonnet IS the planning tier
		}
	case strings.Contains(primaryID, "haiku"):
		return PoolConfig{
			Primary:   primaryID,
			Auxiliary: primaryID, // Haiku IS the auxiliary tier
			Planning:  primaryID, // Single-model mode
		}
	default:
		return PoolConfig{
			Primary:   primaryID,
			Auxiliary: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
			Planning:  "us.anthropic.claude-sonnet-4-6",
		}
	}
}

// EstimateCost calculates the dollar cost for a set of token counts against a specific model.
func (p *ModelPool) EstimateCost(role ModelRole, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) float64 {
	info := p.Info(role)
	cost := float64(inputTokens) * info.InputCost / 1_000_000
	cost += float64(outputTokens) * info.OutputCost / 1_000_000
	cost += float64(cacheReadTokens) * info.CacheReadCost / 1_000_000
	cost += float64(cacheWriteTokens) * info.CacheWriteCost / 1_000_000
	return cost
}
