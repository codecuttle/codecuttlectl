package googleprov

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/provider"
	"google.golang.org/genai"
)

// cacheTTL is the TTL assigned to newly created caches.
// Kept short to minimize storage billing (Google charges per hour stored).
const cacheTTL = 5 * time.Minute

// cacheRefreshBuffer is how far before expiry we proactively refresh.
const cacheRefreshBuffer = 1 * time.Minute

// CacheManager handles the lifecycle of Google AI Context Caches.
// It creates a cache for the static prefix (system prompt + tool definitions)
// when the token count exceeds the configured threshold, refreshes the TTL
// on activity, and deletes the cache on Close().
type CacheManager struct {
	client    *genai.Client
	model     string
	threshold int // minimum token count to justify caching

	mu        sync.Mutex
	cacheName string    // server-assigned cache resource name
	expiresAt time.Time // when the current cache expires
	// Fingerprint of what's cached (to detect when we need to recreate)
	cachedSystemHash string
	cachedToolsHash  string
}

// NewCacheManager creates a cache manager.
// threshold is the minimum estimated token count of system+tools to create a cache.
func NewCacheManager(client *genai.Client, model string, threshold int) *CacheManager {
	if threshold <= 0 {
		threshold = 32000
	}
	return &CacheManager{
		client:    client,
		model:     model,
		threshold: threshold,
	}
}

// hashContent creates a simple fingerprint for change detection.
func hashContent(system string, tools []*genai.Tool) (string, string) {
	sysHash := fmt.Sprintf("%d", len(system))
	toolBytes, _ := json.Marshal(tools)
	toolHash := fmt.Sprintf("%d", len(toolBytes))
	return sysHash, toolHash
}

// EnsureCache checks if a cache should be created or refreshed for the given
// static content. Returns the cache resource name if one is active, or empty
// string if caching is not warranted.
//
// This should be called before each GenerateContent request. It is safe for
// concurrent use.
func (cm *CacheManager) EnsureCache(ctx context.Context, system string, tools []*genai.Tool) (string, error) {
	// Rough token estimate: ~4 chars per token for English text
	estimatedTokens := len(system) / 4
	if tools != nil {
		toolBytes, _ := json.Marshal(tools)
		estimatedTokens += len(toolBytes) / 4
	}

	if estimatedTokens < cm.threshold {
		return "", nil // below threshold, not worth caching
	}

	sysHash, toolHash := hashContent(system, tools)

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// If we have an active cache with matching content, refresh TTL if needed
	if cm.cacheName != "" && cm.cachedSystemHash == sysHash && cm.cachedToolsHash == toolHash {
		if time.Until(cm.expiresAt) > cacheRefreshBuffer {
			// Cache is still fresh
			return cm.cacheName, nil
		}
		// Refresh the TTL
		updated, err := cm.client.Caches.Update(ctx, cm.cacheName, &genai.UpdateCachedContentConfig{
			TTL: cacheTTL,
		})
		if err != nil {
			// If refresh fails, try to recreate
			log.Printf("[google-cache] TTL refresh failed: %v, will recreate", err)
			cm.cacheName = ""
		} else {
			cm.expiresAt = updated.ExpireTime
			return cm.cacheName, nil
		}
	}

	// If content changed or no cache exists, delete old and create new
	if cm.cacheName != "" {
		// Best-effort delete of old cache
		_, _ = cm.client.Caches.Delete(ctx, cm.cacheName, nil)
		cm.cacheName = ""
	}

	// Create new cache
	config := &genai.CreateCachedContentConfig{
		TTL:         cacheTTL,
		DisplayName: "codecuttlectl-session",
	}

	// Add system instruction
	if system != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: system}},
		}
	}

	// Add tools
	if len(tools) > 0 {
		config.Tools = tools
	}

	cached, err := cm.client.Caches.Create(ctx, cm.model, config)
	if err != nil {
		return "", fmt.Errorf("cache create failed: %w", err)
	}

	cm.cacheName = cached.Name
	cm.expiresAt = cached.ExpireTime
	cm.cachedSystemHash = sysHash
	cm.cachedToolsHash = toolHash

	if cached.UsageMetadata != nil {
		log.Printf("[google-cache] Created cache %s (%d tokens, expires %s)",
			cached.Name, cached.UsageMetadata.TotalTokenCount, cached.ExpireTime.Format(time.RFC3339))
	}

	return cm.cacheName, nil
}

// RefreshTTL extends the cache TTL without changing content.
// Call this periodically (e.g., every 3 minutes) to keep the cache alive.
func (cm *CacheManager) RefreshTTL(ctx context.Context) error {
	cm.mu.Lock()
	name := cm.cacheName
	cm.mu.Unlock()

	if name == "" {
		return nil // no active cache
	}

	updated, err := cm.client.Caches.Update(ctx, name, &genai.UpdateCachedContentConfig{
		TTL: cacheTTL,
	})
	if err != nil {
		return fmt.Errorf("cache refresh failed: %w", err)
	}

	cm.mu.Lock()
	cm.expiresAt = updated.ExpireTime
	cm.mu.Unlock()

	return nil
}

// ActiveCacheName returns the current cache name, or empty if none active.
func (cm *CacheManager) ActiveCacheName() string {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.cacheName
}

// Close deletes the active cache to prevent zombie billing.
// Must be called on graceful shutdown.
func (cm *CacheManager) Close(ctx context.Context) error {
	cm.mu.Lock()
	name := cm.cacheName
	cm.cacheName = ""
	cm.mu.Unlock()

	if name == "" {
		return nil
	}

	_, err := cm.client.Caches.Delete(ctx, name, nil)
	if err != nil {
		return fmt.Errorf("cache delete failed: %w", err)
	}
	log.Printf("[google-cache] Deleted cache %s", name)
	return nil
}

// CacheStats returns information about the current cache state.
type CacheStats struct {
	Active    bool
	Name      string
	ExpiresAt time.Time
	TTL       time.Duration
}

// Stats returns the current cache status.
func (cm *CacheManager) Stats() CacheStats {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cacheName == "" {
		return CacheStats{}
	}
	return CacheStats{
		Active:    true,
		Name:      cm.cacheName,
		ExpiresAt: cm.expiresAt,
		TTL:       time.Until(cm.expiresAt),
	}
}

// ---- Integration with Provider ----

// ConverseWithCache wraps a Converse call, creating/using a cache for the
// static prefix (system + tools) and passing the cached content reference
// to GenerateContent.
func (p *Provider) converseWithCache(ctx context.Context, req provider.Request, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	if p.cache == nil {
		// No cache manager — direct call
		contents := toGenAIContents(req.Messages)
		return p.client.Models.GenerateContent(ctx, p.config.Model, contents, config)
	}

	// Attempt to ensure cache for static content
	cacheName, err := p.cache.EnsureCache(ctx, req.System, config.Tools)
	if err != nil {
		// Cache creation failed — fall through to uncached
		log.Printf("[google-cache] EnsureCache error (proceeding uncached): %v", err)
		contents := toGenAIContents(req.Messages)
		return p.client.Models.GenerateContent(ctx, p.config.Model, contents, config)
	}

	contents := toGenAIContents(req.Messages)

	if cacheName != "" {
		// Use cached content: remove system/tools from config (they're in the cache)
		cachedConfig := &genai.GenerateContentConfig{
			CachedContent: cacheName,
		}
		if config.Temperature != nil {
			cachedConfig.Temperature = config.Temperature
		}
		if config.MaxOutputTokens > 0 {
			cachedConfig.MaxOutputTokens = config.MaxOutputTokens
		}
		return p.client.Models.GenerateContent(ctx, p.config.Model, contents, cachedConfig)
	}

	// Below threshold — normal uncached call
	return p.client.Models.GenerateContent(ctx, p.config.Model, contents, config)
}
