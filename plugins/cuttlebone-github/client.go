// Package github provides the GitHub REST API client and token resolution
// for the cuttlebone-github plugin. Token acquisition supports two paths:
//
//  1. GITHUB_TOKEN env var (direct PAT or app token)
//  2. Coder external auth API (fetches OAuth token from Coder agent)
//
// The Coder path enables zero-config GitHub access in Coder workspaces that
// have external auth configured (CODER_EXTERNAL_AUTH_0_TYPE=github).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	githubAPIBase   = "https://api.github.com"
	githubAPIVersion = "2022-11-28"
)

// client handles authenticated GitHub API requests with automatic token resolution.
type client struct {
	httpClient *http.Client

	mu    sync.Mutex
	token string
}

func newClient() *client {
	return &client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// resolveToken returns a valid GitHub token, trying in order:
//  1. GITHUB_TOKEN environment variable
//  2. Coder external auth API (if CODER_AGENT_TOKEN and CODER_AGENT_URL are set)
func (c *client) resolveToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If we already have a cached token, return it.
	// OAuth tokens from Coder are typically valid for the session duration.
	if c.token != "" {
		return c.token, nil
	}

	// Path 1: Direct token from environment
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		c.token = token
		return c.token, nil
	}
	if token := os.Getenv("GH_TOKEN"); token != "" {
		c.token = token
		return c.token, nil
	}

	// Path 2: Coder external auth API
	agentToken := os.Getenv("CODER_AGENT_TOKEN")
	agentURL := os.Getenv("CODER_AGENT_URL")
	if agentToken != "" && agentURL != "" {
		token, err := c.fetchCoderToken(agentURL, agentToken)
		if err != nil {
			return "", fmt.Errorf("coder external auth: %w", err)
		}
		c.token = token
		return c.token, nil
	}

	return "", fmt.Errorf("no GitHub token available. Set GITHUB_TOKEN env var, or run in a Coder workspace with GitHub external auth configured")
}

// coderAuthResponse matches the JSON from Coder's external auth endpoint.
type coderAuthResponse struct {
	AccessToken string `json:"access_token"`
	Username    string `json:"username"`
	Type        string `json:"type"`
}

func (c *client) fetchCoderToken(agentURL, agentToken string) (string, error) {
	url := strings.TrimRight(agentURL, "/") + "/api/v2/workspaceagents/me/external-auth?id=primary-github"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Coder-Session-Token", agentToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("coder auth returned %d: %s", resp.StatusCode, string(body))
	}

	var authResp coderAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if authResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token (GitHub external auth may not be authenticated — visit the Coder dashboard to re-authenticate)")
	}

	return authResp.AccessToken, nil
}

// do performs an authenticated GitHub API request.
// path should start with "/" (e.g., "/repos/owner/repo/pulls").
// body can be nil for GET/DELETE requests.
func (c *client) do(method, path string, body interface{}) (int, json.RawMessage, error) {
	token, err := c.resolveToken()
	if err != nil {
		return 0, nil, err
	}

	url := githubAPIBase + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("marshaling request body: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading response: %w", err)
	}

	return resp.StatusCode, json.RawMessage(respBody), nil
}
