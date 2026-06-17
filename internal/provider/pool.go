package provider

// Pool defines a provider-agnostic multi-model routing interface.
type Pool interface {
	Primary() Provider
	Auxiliary() Provider
	Planning() Provider
	Info(role string) PoolModelInfo
	EstimateCost(role string, input, output, cacheRead, cacheWrite int64) float64
}

// PoolModelInfo contains generic metadata for a model in a pool.
type PoolModelInfo struct {
	DisplayName   string
	ModelID       string
	ContextWindow int32
}
