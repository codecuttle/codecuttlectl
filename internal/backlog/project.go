package backlog

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DetectProject attempts to identify the current project from the working directory.
// Uses a layered approach (first match wins):
//  1. Git remote origin → extract repo name
//  2. Go module path from go.mod → last path segment
//  3. package.json name field
//  4. Basename of the working directory
//
// Returns empty string if detection fails entirely.
func DetectProject(workDir string) string {
	if workDir == "" {
		return ""
	}

	// 1. Git remote origin
	if name := detectFromGitRemote(workDir); name != "" {
		return name
	}

	// 2. Go module path
	if name := detectFromGoMod(workDir); name != "" {
		return name
	}

	// 3. package.json
	if name := detectFromPackageJSON(workDir); name != "" {
		return name
	}

	// 4. Fallback: directory basename
	return filepath.Base(workDir)
}

// detectFromGitRemote extracts the repo name from git remote origin URL.
// Handles both SSH (git@github.com:org/repo.git) and HTTPS (https://github.com/org/repo.git).
func detectFromGitRemote(workDir string) string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	url := strings.TrimSpace(string(out))
	if url == "" {
		return ""
	}

	// Strip .git suffix
	url = strings.TrimSuffix(url, ".git")

	// SSH format: git@github.com:org/repo
	if idx := strings.LastIndex(url, ":"); idx >= 0 && !strings.Contains(url[idx:], "/") == false {
		// Has colon followed by path — SSH format
		path := url[idx+1:]
		if lastSlash := strings.LastIndex(path, "/"); lastSlash >= 0 {
			return path[lastSlash+1:]
		}
		return path
	}

	// HTTPS format: https://github.com/org/repo
	if lastSlash := strings.LastIndex(url, "/"); lastSlash >= 0 {
		return url[lastSlash+1:]
	}

	return ""
}

// detectFromGoMod extracts the module name from go.mod.
func detectFromGoMod(workDir string) string {
	data, err := os.ReadFile(filepath.Join(workDir, "go.mod"))
	if err != nil {
		return ""
	}

	// Find the module line: "module github.com/org/repo"
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			modulePath := strings.TrimPrefix(line, "module ")
			modulePath = strings.TrimSpace(modulePath)
			// Return last path segment
			if lastSlash := strings.LastIndex(modulePath, "/"); lastSlash >= 0 {
				return modulePath[lastSlash+1:]
			}
			return modulePath
		}
	}

	return ""
}

// detectFromPackageJSON extracts the name field from package.json.
func detectFromPackageJSON(workDir string) string {
	data, err := os.ReadFile(filepath.Join(workDir, "package.json"))
	if err != nil {
		return ""
	}

	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}

	name := pkg.Name
	// Handle scoped packages: @org/name → name
	if strings.HasPrefix(name, "@") {
		if idx := strings.Index(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
	}

	return name
}
