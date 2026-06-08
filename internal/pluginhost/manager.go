// Package pluginhost implements the HashiCorp go-plugin host (orchestrator side)
// for managing Cuttlebone tool plugins as isolated subprocesses.
//
// Robustness guarantees:
//   - Startup timeout: plugins that fail to handshake within 10s are skipped
//   - Execution timeout: per-plugin max timeout (from Describe), default 120s
//   - Crash recovery: if a plugin process dies mid-execution, it is automatically restarted
//   - Graceful degradation: a crashed/unavailable plugin returns an error to the model
//     rather than crashing the orchestrator
package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	pluginschema "github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
	"github.com/codecuttle/codecuttlectl/internal/skills"
)

// Handshake is the shared handshake config between host and plugins.
// Both sides must agree on these values for a plugin to be accepted.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "CUTTLEBONE_PLUGIN",
	MagicCookieValue: "codecuttle-v1",
}

// PluginMap is the map of plugin types that go-plugin uses to negotiate.
var PluginMap = map[string]plugin.Plugin{
	"tool": &ToolGRPCPlugin{},
}

const (
	// DefaultStartupTimeout is how long we wait for a plugin to start and handshake.
	DefaultStartupTimeout = 10 * time.Second
	// DefaultExecTimeout is the fallback execution timeout if the plugin doesn't specify one.
	DefaultExecTimeout = 120 * time.Second
	// MaxRestartAttempts is how many times we'll try to restart a crashed plugin.
	MaxRestartAttempts = 3
)

// ToolGRPCPlugin implements plugin.GRPCPlugin for the ToolPlugin service.
type ToolGRPCPlugin struct {
	plugin.Plugin
	// Impl is only set on the plugin (server) side.
	Impl ToolPluginServer
}

func (p *ToolGRPCPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterToolPluginServer(s, &grpcServer{impl: p.Impl})
	return nil
}

func (p *ToolGRPCPlugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &grpcClient{client: pb.NewToolPluginClient(c)}, nil
}

// ToolPluginServer is the interface that plugin implementations must satisfy.
type ToolPluginServer interface {
	Describe(ctx context.Context) (*pb.DescribeResponse, error)
	Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error)
}

// --- gRPC Server (plugin side) ---

type grpcServer struct {
	pb.UnimplementedToolPluginServer
	impl ToolPluginServer
}

func (s *grpcServer) Describe(ctx context.Context, req *pb.DescribeRequest) (*pb.DescribeResponse, error) {
	return s.impl.Describe(ctx)
}

func (s *grpcServer) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	return s.impl.Execute(ctx, req)
}

// --- gRPC Client (host side) ---

type grpcClient struct {
	client pb.ToolPluginClient
}

func (c *grpcClient) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return c.client.Describe(ctx, &pb.DescribeRequest{})
}

func (c *grpcClient) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	return c.client.Execute(ctx, req)
}

// --- Plugin Manager ---

// ManagedPlugin holds a running plugin's client and its metadata.
type ManagedPlugin struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	LLMHint     string
	Version     string
	Path        string // Binary path for restart
	MaxTimeout  time.Duration

	client    *plugin.Client
	rpcClient *grpcClient

	// Crash recovery state
	restarts int
	healthy  bool
}

// Manager discovers, launches, and manages tool plugin subprocesses.
type Manager struct {
	mu            sync.RWMutex
	plugins       map[string]*ManagedPlugin
	logger        hclog.Logger
	skills        *skills.Registry
	validateInput bool // When true, validate tool input against schema before execution
}

// NewManager creates a new plugin manager.
func NewManager(verbose bool) *Manager {
	level := hclog.Error
	if verbose {
		level = hclog.Debug
	}
	return &Manager{
		plugins: make(map[string]*ManagedPlugin),
		logger: hclog.New(&hclog.LoggerOptions{
			Name:   "cuttlebone",
			Level:  level,
			Output: os.Stderr,
		}),
		skills:        skills.NewRegistry(skills.DefaultBudget),
		validateInput: true, // Enabled by default
	}
}

// DiscoverPlugins scans a directory for plugin binaries and starts them.
// Plugin binaries must be named with the prefix "cuttlebone-".
// Plugins that fail to start within the startup timeout are skipped (not fatal).
func (m *Manager) DiscoverPlugins(ctx context.Context, pluginDir string) error {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No plugin directory is fine
		}
		return fmt.Errorf("reading plugin directory %s: %w", pluginDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) < 11 || name[:11] != "cuttlebone-" {
			continue
		}

		path := filepath.Join(pluginDir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// Check if executable
		if info.Mode()&0111 == 0 {
			continue
		}

		if err := m.LoadPlugin(ctx, path); err != nil {
			m.logger.Error("failed to load plugin", "path", path, "error", err)
			continue
		}
	}
	return nil
}

// LoadPlugin starts a single plugin binary and registers it.
// Applies a startup timeout to prevent hanging on broken plugins.
func (m *Manager) LoadPlugin(ctx context.Context, path string) error {
	// Apply startup timeout
	startCtx, cancel := context.WithTimeout(ctx, DefaultStartupTimeout)
	defer cancel()

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginMap,
		Cmd:             exec.Command(path),
		AllowedProtocols: []plugin.Protocol{
			plugin.ProtocolGRPC,
		},
		Logger: m.logger,
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return fmt.Errorf("connecting to plugin %s: %w", path, err)
	}

	raw, err := rpcClient.Dispense("tool")
	if err != nil {
		client.Kill()
		return fmt.Errorf("dispensing tool interface from %s: %w", path, err)
	}

	toolClient, ok := raw.(*grpcClient)
	if !ok {
		client.Kill()
		return fmt.Errorf("plugin %s does not implement tool interface", path)
	}

	// Describe the plugin to get its metadata (with startup timeout)
	desc, err := toolClient.Describe(startCtx)
	if err != nil {
		client.Kill()
		return fmt.Errorf("describing plugin %s: %w", path, err)
	}

	// Determine max timeout from plugin capabilities
	maxTimeout := DefaultExecTimeout
	if desc.Capabilities != nil && desc.Capabilities.MaxTimeoutSeconds > 0 {
		maxTimeout = time.Duration(desc.Capabilities.MaxTimeoutSeconds) * time.Second
	}

	managed := &ManagedPlugin{
		Name:        desc.Name,
		Description: desc.Description,
		InputSchema: json.RawMessage(desc.InputSchema),
		LLMHint:     desc.LlmContextHint,
		Version:     desc.Version,
		Path:        path,
		MaxTimeout:  maxTimeout,
		client:      client,
		rpcClient:   toolClient,
		healthy:     true,
	}

	m.mu.Lock()
	m.plugins[desc.Name] = managed
	m.mu.Unlock()

	// Register skills from this plugin
	if len(desc.Skills) > 0 {
		m.skills.Register(desc.Name, desc.Version, desc.Skills)
		m.logger.Info("registered skills", "plugin", desc.Name, "count", len(desc.Skills))
	}

	m.logger.Info("loaded plugin", "name", desc.Name, "version", desc.Version, "path", path, "timeout", maxTimeout)
	return nil
}

// ExecuteResult holds the full result of a plugin execution including metadata.
type ExecuteResult struct {
	Output   string
	Metadata map[string]string
	IsError  bool
}

// Execute invokes a tool by name with the given JSON input.
// Applies a per-plugin execution timeout.
// If the plugin has crashed, attempts to restart it before failing.
func (m *Manager) Execute(ctx context.Context, name string, input json.RawMessage, workDir string) (string, error) {
	result, err := m.ExecuteFull(ctx, name, input, workDir)
	if err != nil {
		return result.Output, err
	}
	if result.IsError {
		return result.Output, fmt.Errorf("tool error")
	}
	return result.Output, nil
}

// ExecuteFull invokes a tool and returns the full result including metadata.
// Use this when you need access to stderr, exit codes, or other metadata
// for telemetry/Inkwell recording.
func (m *Manager) ExecuteFull(ctx context.Context, name string, input json.RawMessage, workDir string) (ExecuteResult, error) {
	m.mu.RLock()
	p, ok := m.plugins[name]
	m.mu.RUnlock()

	if !ok {
		return ExecuteResult{Output: fmt.Sprintf("unknown plugin tool: %s", name)}, fmt.Errorf("unknown plugin tool: %s", name)
	}

	// Check if the plugin process is still alive; restart if needed
	if !p.healthy || p.client.Exited() {
		if err := m.restartPlugin(ctx, p); err != nil {
			return ExecuteResult{Output: fmt.Sprintf("Plugin %s is unavailable: %s", name, err.Error()), IsError: true},
				fmt.Errorf("plugin %s crashed and could not be restarted: %w", name, err)
		}
	}

	// Validate input against the plugin's declared schema (if validation is enabled)
	if m.validateInput && len(p.InputSchema) > 0 {
		if err := pluginschema.Validate(string(p.InputSchema), input); err != nil {
			errMsg := fmt.Sprintf("Input validation failed for tool %s: %s", name, err.Error())
			m.logger.Debug("input validation failed", "tool", name, "error", err)
			return ExecuteResult{Output: errMsg, IsError: true}, fmt.Errorf("input validation: %w", err)
		}
	}

	// Apply execution timeout
	execCtx, cancel := context.WithTimeout(ctx, p.MaxTimeout)
	defer cancel()

	resp, err := p.rpcClient.Execute(execCtx, &pb.ExecuteRequest{
		Input:            string(input),
		WorkingDirectory: workDir,
	})
	if err != nil {
		// Check if this is a crash (connection lost)
		if p.client.Exited() {
			p.healthy = false
			m.logger.Warn("plugin crashed during execution", "name", name, "error", err)

			// Attempt restart and retry once
			if restartErr := m.restartPlugin(ctx, p); restartErr == nil {
				// Retry the execution on the fresh process
				retryCtx, retryCancel := context.WithTimeout(ctx, p.MaxTimeout)
				defer retryCancel()
				resp, err = p.rpcClient.Execute(retryCtx, &pb.ExecuteRequest{
					Input:            string(input),
					WorkingDirectory: workDir,
				})
				if err != nil {
					return ExecuteResult{Output: fmt.Sprintf("Plugin %s failed after restart: %s", name, err.Error()), IsError: true},
						fmt.Errorf("executing plugin %s after restart: %w", name, err)
				}
			} else {
				return ExecuteResult{Output: fmt.Sprintf("Plugin %s crashed and could not be restarted: %s", name, restartErr.Error()), IsError: true},
					fmt.Errorf("plugin %s unavailable: %w", name, restartErr)
			}
		} else if execCtx.Err() == context.DeadlineExceeded {
			return ExecuteResult{Output: fmt.Sprintf("Plugin %s timed out after %s", name, p.MaxTimeout), IsError: true},
				fmt.Errorf("plugin %s execution timed out after %s", name, p.MaxTimeout)
		} else {
			return ExecuteResult{Output: fmt.Sprintf("Plugin %s error: %s", name, err.Error()), IsError: true},
				fmt.Errorf("executing plugin %s: %w", name, err)
		}
	}

	if resp.IsError {
		// Return the error message as content so the model can reason about it
		errMsg := resp.ErrorMessage
		if resp.Output != "" {
			errMsg = resp.Output + "\n" + errMsg
		}
		return ExecuteResult{Output: errMsg, Metadata: resp.Metadata, IsError: true}, fmt.Errorf("tool error: %s", resp.ErrorMessage)
	}

	return ExecuteResult{Output: resp.Output, Metadata: resp.Metadata, IsError: false}, nil
}

// restartPlugin attempts to restart a crashed plugin subprocess.
func (m *Manager) restartPlugin(ctx context.Context, p *ManagedPlugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p.restarts >= MaxRestartAttempts {
		return fmt.Errorf("plugin %s exceeded max restart attempts (%d)", p.Name, MaxRestartAttempts)
	}

	m.logger.Info("restarting plugin", "name", p.Name, "path", p.Path, "attempt", p.restarts+1)

	// Kill any lingering process
	if p.client != nil {
		p.client.Kill()
	}

	// Start a fresh process
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginMap,
		Cmd:             exec.Command(p.Path),
		AllowedProtocols: []plugin.Protocol{
			plugin.ProtocolGRPC,
		},
		Logger: m.logger,
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		p.restarts++
		return fmt.Errorf("reconnecting to plugin %s: %w", p.Name, err)
	}

	raw, err := rpcClient.Dispense("tool")
	if err != nil {
		client.Kill()
		p.restarts++
		return fmt.Errorf("dispensing tool from restarted plugin %s: %w", p.Name, err)
	}

	toolClient, ok := raw.(*grpcClient)
	if !ok {
		client.Kill()
		p.restarts++
		return fmt.Errorf("restarted plugin %s does not implement tool interface", p.Name)
	}

	// Verify the restarted plugin is healthy
	startCtx, cancel := context.WithTimeout(ctx, DefaultStartupTimeout)
	defer cancel()
	if _, err := toolClient.Describe(startCtx); err != nil {
		client.Kill()
		p.restarts++
		return fmt.Errorf("health check failed for restarted plugin %s: %w", p.Name, err)
	}

	// Update the managed plugin with new connections
	p.client = client
	p.rpcClient = toolClient
	p.healthy = true
	p.restarts++

	m.logger.Info("plugin restarted successfully", "name", p.Name, "total_restarts", p.restarts)
	return nil
}

// IsHealthy returns whether a specific plugin is currently responsive.
func (m *Manager) IsHealthy(name string) bool {
	m.mu.RLock()
	p, ok := m.plugins[name]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	return p.healthy && !p.client.Exited()
}

// HealthStatus returns a map of plugin names to their health status.
func (m *Manager) HealthStatus() map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make(map[string]bool, len(m.plugins))
	for name, p := range m.plugins {
		status[name] = p.healthy && !p.client.Exited()
	}
	return status
}

// Definitions returns bedrock-compatible tool definitions for all loaded plugins.
func (m *Manager) Definitions() []bedrock.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var defs []bedrock.ToolDefinition
	for _, p := range m.plugins {
		defs = append(defs, bedrock.ToolDefinition{
			Name:        p.Name,
			Description: p.Description,
			InputSchema: p.InputSchema,
		})
	}
	return defs
}

// PluginNames returns the names of all loaded plugins.
func (m *Manager) PluginNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var names []string
	for name := range m.plugins {
		names = append(names, name)
	}
	return names
}

// LLMHints returns concatenated LLM context hints from all plugins.
func (m *Manager) LLMHints() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var hints string
	for _, p := range m.plugins {
		if p.LLMHint != "" {
			hints += "\n\n" + p.LLMHint
		}
	}
	return hints
}

// Shutdown kills all running plugin subprocesses.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, p := range m.plugins {
		m.logger.Debug("killing plugin", "name", name)
		p.client.Kill()
	}
	m.plugins = make(map[string]*ManagedPlugin)
}

// Count returns the number of loaded plugins.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.plugins)
}

// Skills returns the skill registry.
func (m *Manager) Skills() *skills.Registry {
	return m.skills
}

// SetValidateInput enables or disables input schema validation before tool execution.
// When enabled (default), tool inputs are validated against the plugin's declared
// JSON Schema before being sent to the plugin. Validation failures are returned
// to the LLM as tool errors so it can fix its input.
func (m *Manager) SetValidateInput(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.validateInput = enabled
}
