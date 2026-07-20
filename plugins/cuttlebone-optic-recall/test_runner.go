package main

import (
	"context"
	"fmt"
	"os"
	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
)

func runTest() {
	tool := &opticRecallTool{}
	inputJSON := `{"query": "find semantic block", "repo_id": "123e4567-e89b-12d3-a456-426614174000", "max_hops": 2, "limit": 5}`
	
	req := &pb.ExecuteRequest{
		Input: inputJSON,
	}

	ctx := context.Background()
	res, err := tool.Execute(ctx, req)
	if err != nil {
		fmt.Printf("Execute error: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Printf("IsError: %v\n", res.IsError)
	if res.IsError {
		fmt.Printf("ErrorMessage: %s\n", res.ErrorMessage)
	} else {
		fmt.Printf("Output:\n%s\n", res.Output)
	}
}
