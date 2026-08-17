// cuttlebone-grep is a Cuttlebone plugin that searches file contents using regex.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"
)

type grepTool struct{}

type grepInput struct {
	Pattern    string        `json:"pattern" jsonschema:"required" jsonschema_description:"Regular expression pattern to search for in file contents"`
	Path       string        `json:"path,omitempty" jsonschema_description:"Directory to search in. Defaults to working directory."`
	Include    string        `json:"include,omitempty" jsonschema_description:"File glob pattern to filter which files to search (e.g. '*.go', '*.{ts,tsx}')"`
	MaxResults types.FlexInt `json:"max_results,omitempty" jsonschema_description:"Maximum number of matching lines to return. Default: 50"`
}

func (t *grepTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:           "grep",
		Description:    "Search file contents using a regular expression pattern. Returns matching file paths and line numbers. Optionally filter by file name pattern (glob). Searches recursively from the given directory.",
		InputSchema:    schema.MustSchema(&grepInput{}),
		LlmContextHint: "Use grep to find files containing specific code patterns, function definitions, variable usages, or error messages. Supports full regex syntax. Use the include parameter to narrow results to specific file types.",
		Version:        "1.0.0",
		CommandPatterns: []string{
			"grep *",
			"rg *",
			"ag *",
		},
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			SupportsStreaming:    true,
			MaxTimeoutSeconds:    30,
		},
	}, nil
}

func (t *grepTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params grepInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.Pattern == "" {
		return &pb.ExecuteResponse{IsError: true, ErrorMessage: "pattern is required"}, nil
	}

	re, err := regexp.Compile(params.Pattern)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("invalid regex: %v", err),
		}, nil
	}

	searchDir := params.Path
	if searchDir == "" {
		searchDir = req.WorkingDirectory
	}
	if searchDir == "" {
		searchDir = "."
	}

	maxResults := params.MaxResults.Int()
	if maxResults <= 0 {
		maxResults = 50
	}

	var results []string
	matchCount := 0

	err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable entries
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if info.IsDir() {
			// Skip hidden directories and common non-code directories
			name := info.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary/large files
		if info.Size() > 1024*1024 { // Skip files > 1MB
			return nil
		}

		// Apply include filter
		if params.Include != "" {
			matched, _ := filepath.Match(params.Include, info.Name())
			if !matched {
				// Try with brace expansion for patterns like *.{go,ts}
				// Simple approach: just match the base name
				return nil
			}
		}

		// Skip binary files by extension
		ext := strings.ToLower(filepath.Ext(path))
		if isBinaryExt(ext) {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				relPath, _ := filepath.Rel(searchDir, path)
				if relPath == "" {
					relPath = path
				}
				results = append(results, fmt.Sprintf("%s:%d: %s", relPath, lineNum, truncateLine(line, 200)))
				matchCount++
				if matchCount >= maxResults {
					return fmt.Errorf("max_results reached")
				}
			}
		}
		return nil
	})

	if err != nil && err.Error() != "max_results reached" && ctx.Err() == nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("search error: %v", err),
		}, nil
	}

	if len(results) == 0 {
		return &pb.ExecuteResponse{
			Output:   fmt.Sprintf("No matches found for pattern %q", params.Pattern),
			Metadata: map[string]string{"matches": "0"},
		}, nil
	}

	output := strings.Join(results, "\n")
	if matchCount >= maxResults {
		output += fmt.Sprintf("\n\n(showing first %d matches, more may exist)", maxResults)
	}

	return &pb.ExecuteResponse{
		Output:   output,
		Metadata: map[string]string{"matches": fmt.Sprintf("%d", matchCount)},
	}, nil
}

func truncateLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func isBinaryExt(ext string) bool {
	binary := map[string]bool{
		".exe": true, ".bin": true, ".so": true, ".dylib": true, ".dll": true,
		".o": true, ".a": true, ".class": true, ".jar": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
		".pdf": true, ".zip": true, ".tar": true, ".gz": true, ".bz2": true,
		".wasm": true, ".pyc": true, ".pyo": true,
	}
	return binary[ext]
}

// ExecuteStream implements the streaming execution RPC for grep.
// Streams matching lines incrementally as files are scanned.
func (t *grepTool) ExecuteStream(req *pb.ExecuteRequest, stream pb.ToolPlugin_ExecuteStreamServer) error {
	var params grepInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("parsing input: %v", err),
			}},
		})
	}

	if params.Pattern == "" {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: "pattern is required",
			}},
		})
	}

	re, err := regexp.Compile(params.Pattern)
	if err != nil {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("invalid regex: %v", err),
			}},
		})
	}

	searchDir := params.Path
	if searchDir == "" {
		searchDir = req.WorkingDirectory
	}
	if searchDir == "" {
		searchDir = "."
	}

	maxResults := params.MaxResults.Int()
	if maxResults <= 0 {
		maxResults = 50
	}

	ctx := stream.Context()
	var results []string
	matchCount := 0

	err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if info.IsDir() {
			name := info.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > 1024*1024 {
			return nil
		}
		if params.Include != "" {
			matched, _ := filepath.Match(params.Include, info.Name())
			if !matched {
				return nil
			}
		}
		ext := strings.ToLower(filepath.Ext(path))
		if isBinaryExt(ext) {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				relPath, _ := filepath.Rel(searchDir, path)
				if relPath == "" {
					relPath = path
				}
				matchLine := fmt.Sprintf("%s:%d: %s", relPath, lineNum, truncateLine(line, 200))
				results = append(results, matchLine)

				// Stream each match as an output delta
				stream.Send(&pb.ExecuteStreamEvent{
					Event: &pb.ExecuteStreamEvent_OutputDelta{OutputDelta: &pb.OutputDelta{Text: matchLine + "\n"}},
				})

				matchCount++
				if matchCount >= maxResults {
					return fmt.Errorf("max_results reached")
				}
			}
		}
		return nil
	})

	if err != nil && err.Error() != "max_results reached" && ctx.Err() == nil {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("search error: %v", err),
			}},
		})
	}

	// Build final output
	var output string
	if len(results) == 0 {
		output = fmt.Sprintf("No matches found for pattern %q", params.Pattern)
	} else {
		output = strings.Join(results, "\n")
		if matchCount >= maxResults {
			output += fmt.Sprintf("\n\n(showing first %d matches, more may exist)", maxResults)
		}
	}

	return stream.Send(&pb.ExecuteStreamEvent{
		Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
			Output:   output,
			Metadata: map[string]string{"matches": fmt.Sprintf("%d", matchCount)},
		}},
	})
}

func main() {
	pluginkit.Serve(&grepTool{})
}
