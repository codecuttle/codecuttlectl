// Package pluginkit provides helpers for building Cuttlebone tool plugins.
// Plugin authors import this package and call Serve() with their implementation.
package pluginkit

import (
	"context"
	"embed"

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

// StreamingToolImpl is an optional interface that plugins can implement
// to support streaming output. Plugins that implement this interface should
// also set SupportsStreaming=true in their ToolCapabilities.
type StreamingToolImpl interface {
	ToolImpl
	ExecuteStream(req *pb.ExecuteRequest, stream pb.ToolPlugin_ExecuteStreamServer) error
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

func (s *server) ExecuteStream(req *pb.ExecuteRequest, stream pb.ToolPlugin_ExecuteStreamServer) error {
	// If the implementation supports streaming, use it
	if streamer, ok := s.impl.(StreamingToolImpl); ok {
		return streamer.ExecuteStream(req, stream)
	}
	// Fallback: run Execute and send as a single final event
	resp, err := s.impl.Execute(stream.Context(), req)
	if err != nil {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: err.Error(),
			}},
		})
	}
	return stream.Send(&pb.ExecuteStreamEvent{
		Event: &pb.ExecuteStreamEvent_Final{Final: resp},
	})
}

// EmbedSkill creates a Skill proto from an embedded file. This is the recommended
// way for plugin authors to ship skills alongside their tools.
//
// Usage:
//
//	//go:embed skills/*
//	var skillFS embed.FS
//
//	skills: []*pb.Skill{
//	    pluginkit.EmbedSkill(skillFS, "skills/debugging.md", "my_debugging", "on_error:*", 50),
//	}
func EmbedSkill(fs embed.FS, path, name, trigger string, priority int) *pb.Skill {
	data, err := fs.ReadFile(path)
	if err != nil {
		// If the file can't be read, return an empty skill that won't fire
		return &pb.Skill{
			Name:    name,
			Trigger: "on_request",
			Content: "(skill content unavailable: " + err.Error() + ")",
		}
	}

	content := string(data)
	estimatedTokens := int32(len(content) / 4)

	return &pb.Skill{
		Name:            name,
		Trigger:         trigger,
		ContentType:     "markdown",
		Content:         content,
		Priority:        int32(priority),
		EstimatedTokens: estimatedTokens,
	}
}

// NewSkill creates a Skill proto from a string. For inline skill content.
func NewSkill(name, trigger, contentType, content string, priority int) *pb.Skill {
	return &pb.Skill{
		Name:            name,
		Trigger:         trigger,
		ContentType:     contentType,
		Content:         content,
		Priority:        int32(priority),
		EstimatedTokens: int32(len(content) / 4),
	}
}
