// cuttlebone-glob is a Cuttlebone plugin that finds files by name pattern.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

type globTool struct{}

func (t *globTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "glob",
		Description: "Find files matching a glob pattern. Supports patterns like '**/*.go', 'src/**/*.ts', '*.md'. Returns matching file paths sorted by modification time (most recent first).",
		InputSchema: `{
			"type": "object",
			"properties": {
				"pattern": {
					"type": "string",
					"description": "Glob pattern to match files (e.g. '**/*.go', 'src/**/*.ts', 'Makefile')"
				},
				"path": {
					"type": "string",
					"description": "Base directory to search from. Defaults to working directory."
				},
				"max_results": {
					"type": "integer",
					"description": "Maximum number of results. Default: 100"
				}
			},
			"required": ["pattern"]
		}`,
		LlmContextHint: "Use glob to find files by name pattern before reading or editing them. Useful for discovering project structure, finding config files, or locating all files of a specific type.",
		Version:         "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    15,
		},
	}, nil
}

type globInput struct {
	Pattern    string  `json:"pattern"`
	Path       string  `json:"path,omitempty"`
	MaxResults flexInt `json:"max_results,omitempty"`
}

type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	var i int
	if err := json.Unmarshal(data, &i); err == nil {
		*f = flexInt(i)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		var i2 int
		fmt.Sscanf(s, "%d", &i2)
		*f = flexInt(i2)
		return nil
	}
	*f = 0
	return nil
}

type fileResult struct {
	path    string
	modTime int64
}

func (t *globTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params globInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.Pattern == "" {
		return &pb.ExecuteResponse{IsError: true, ErrorMessage: "pattern is required"}, nil
	}

	baseDir := params.Path
	if baseDir == "" {
		baseDir = req.WorkingDirectory
	}
	if baseDir == "" {
		baseDir = "."
	}

	maxResults := int(params.MaxResults)
	if maxResults <= 0 {
		maxResults = 100
	}

	var matches []fileResult

	// Handle ** patterns by walking the tree
	if strings.Contains(params.Pattern, "**") {
		// Split pattern into dir prefix and file pattern
		err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if info.IsDir() {
				name := info.Name()
				if name != "." && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
					return filepath.SkipDir
				}
				return nil
			}

			relPath, _ := filepath.Rel(baseDir, path)
			if matchGlob(params.Pattern, relPath) {
				matches = append(matches, fileResult{path: relPath, modTime: info.ModTime().Unix()})
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			return &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("walking directory: %v", err),
			}, nil
		}
	} else {
		// Simple glob without **
		fullPattern := filepath.Join(baseDir, params.Pattern)
		globMatches, err := filepath.Glob(fullPattern)
		if err != nil {
			return &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("invalid glob pattern: %v", err),
			}, nil
		}
		for _, m := range globMatches {
			info, err := os.Stat(m)
			if err != nil {
				continue
			}
			if info.IsDir() {
				continue
			}
			relPath, _ := filepath.Rel(baseDir, m)
			matches = append(matches, fileResult{path: relPath, modTime: info.ModTime().Unix()})
		}
	}

	// Sort by modification time (most recent first)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime > matches[j].modTime
	})

	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}

	if len(matches) == 0 {
		return &pb.ExecuteResponse{
			Output:   fmt.Sprintf("No files found matching pattern %q", params.Pattern),
			Metadata: map[string]string{"matches": "0"},
		}, nil
	}

	var lines []string
	for _, m := range matches {
		lines = append(lines, m.path)
	}

	return &pb.ExecuteResponse{
		Output:   strings.Join(lines, "\n"),
		Metadata: map[string]string{"matches": fmt.Sprintf("%d", len(matches))},
	}, nil
}

// matchGlob implements a simplified ** glob matching.
func matchGlob(pattern, path string) bool {
	// Normalize separators
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	// Handle **/ prefix
	if strings.HasPrefix(pattern, "**/") {
		filePattern := pattern[3:]
		// Match against any path suffix
		parts := strings.Split(path, "/")
		for i := range parts {
			suffix := strings.Join(parts[i:], "/")
			if matched, _ := filepath.Match(filePattern, suffix); matched {
				return true
			}
			// Also try matching just the filename against the pattern
			if matched, _ := filepath.Match(filePattern, parts[len(parts)-1]); matched {
				return true
			}
		}
		return false
	}

	// Handle patterns with ** in the middle
	if strings.Contains(pattern, "/**/") {
		parts := strings.SplitN(pattern, "/**/", 2)
		prefix := parts[0]
		suffix := parts[1]

		// Path must start with prefix
		if !strings.HasPrefix(path, prefix+"/") && path != prefix {
			return false
		}

		// Remaining path must match suffix
		remaining := strings.TrimPrefix(path, prefix+"/")
		if matched, _ := filepath.Match(suffix, filepath.Base(remaining)); matched {
			return true
		}
		return false
	}

	// Fall back to simple match
	matched, _ := filepath.Match(pattern, path)
	if matched {
		return true
	}
	// Try matching just the filename
	matched, _ = filepath.Match(pattern, filepath.Base(path))
	return matched
}

func main() {
	pluginkit.Serve(&globTool{})
}
