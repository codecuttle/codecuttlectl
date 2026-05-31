// Package pluginkit provides helpers for building Cuttlebone tool plugins.
// Plugin authors import this package and call Serve() with their implementation.
package pluginkit

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
)

// ToolImpl is the interface that plugin authors implement.
type ToolImpl interface {
	Describe(ctx context.Context) (*pb.DescribeResponse, error)
	Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error)
}

// Serve starts the gRPC plugin server. Call this from your plugin's main().
func Serve(impl ToolImpl) {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pluginhost.Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &toolPlugin{impl: impl},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}

// toolPlugin wraps the user's implementation for go-plugin.
type toolPlugin struct {
	plugin.Plugin
	impl ToolImpl
}

func (p *toolPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterToolPluginServer(s, &server{impl: p.impl})
	return nil
}

func (p *toolPlugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	// Client side is handled by pluginhost
	return nil, nil
}

type server struct {
	pb.UnimplementedToolPluginServer
	impl ToolImpl
}

func (s *server) Describe(ctx context.Context, req *pb.DescribeRequest) (*pb.DescribeResponse, error) {
	return s.impl.Describe(ctx)
}

func (s *server) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	return s.impl.Execute(ctx, req)
}
