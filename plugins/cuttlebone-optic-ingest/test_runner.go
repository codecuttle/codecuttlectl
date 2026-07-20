package main

import (
	"context"
	"fmt"
	"os"
	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
)

func runTest() {
	tool := &opticIngestTool{}
	inputJSON := `{"repo_id": "123e4567-e89b-12d3-a456-426614174000", "commit_hash": "bd3311e492320feda4ac271ed152b3b4292013fa", "commit_message": "test msg", "author_id": "123e4567-e89b-12d3-a456-426614174001"}`
	
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
		fmt.Printf("Output: %s\n", res.Output)
	}
}
