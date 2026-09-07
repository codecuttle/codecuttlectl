// runtime-plugin is an inert test fixture, never an installed git implementation.
// It does not execute shell commands or git; each call appends to a temp-workspace
// log so approval tests can independently count actual executions.
package main

import (
	"context"
	"os"
	"path/filepath"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

type fixture struct{}

func (*fixture) Describe(context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name: "git", Version: "test-only", Description: "Inert approval test fixture; never executes git",
		InputSchema: `{"type":"object","properties":{"subcommand":{"type":"string"},"args":{"type":"array","items":{"type":"string"}}},"required":["subcommand"]}`,
	}, nil
}

func (*fixture) Execute(_ context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	f, err := os.OpenFile(filepath.Join(req.WorkingDirectory, "fixture-calls.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, writeErr := f.WriteString(req.Input + "\n")
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return &pb.ExecuteResponse{Output: "INERT_GIT_OK"}, nil
}

func main() { pluginkit.Serve(&fixture{}) }
