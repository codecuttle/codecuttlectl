// Package pluginhost implements the HashiCorp go-plugin host (orchestrator side)
// for managing Cuttlebone tool plugins as isolated subprocesses.
package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
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
	client      *plugin.Client
	rpcClient   *grpcClient
}

// Manager discovers, launches, and manages tool plugin subprocesses.
type Manager struct {
	mu      sync.RWMutex
	plugins map[string]*ManagedPlugin
	logger  hclog.Logger
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
	}
}

// DiscoverPlugins scans a directory for plugin binaries and starts them.
// Plugin binaries must be named with the prefix "cuttlebone-".
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
func (m *Manager) LoadPlugin(ctx context.Context, path string) error {
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

	// Describe the plugin to get its metadata
	desc, err := toolClient.Describe(ctx)
	if err != nil {
		client.Kill()
		return fmt.Errorf("describing plugin %s: %w", path, err)
	}

	managed := &ManagedPlugin{
		Name:        desc.Name,
		Description: desc.Description,
		InputSchema: json.RawMessage(desc.InputSchema),
		LLMHint:     desc.LlmContextHint,
		Version:     desc.Version,
		client:      client,
		rpcClient:   toolClient,
	}

	m.mu.Lock()
	m.plugins[desc.Name] = managed
	m.mu.Unlock()

	m.logger.Info("loaded plugin", "name", desc.Name, "version", desc.Version, "path", path)
	return nil
}

// Execute invokes a tool by name with the given JSON input.
func (m *Manager) Execute(ctx context.Context, name string, input json.RawMessage, workDir string) (string, error) {
	m.mu.RLock()
	p, ok := m.plugins[name]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("unknown plugin tool: %s", name)
	}

	resp, err := p.rpcClient.Execute(ctx, &pb.ExecuteRequest{
		Input:            string(input),
		WorkingDirectory: workDir,
	})
	if err != nil {
		return "", fmt.Errorf("executing plugin %s: %w", name, err)
	}

	if resp.IsError {
		// Return the error message as content (not a Go error) so the model can see
		// and reason about tool-level errors (file not found, permission denied, etc.)
		errMsg := resp.ErrorMessage
		if resp.Output != "" {
			errMsg = resp.Output + "\n" + errMsg
		}
		return errMsg, fmt.Errorf("tool error: %s", resp.ErrorMessage)
	}

	return resp.Output, nil
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
