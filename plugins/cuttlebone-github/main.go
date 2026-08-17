// cuttlebone-github is a Cuttlebone plugin that provides GitHub operations
// via the REST API. Supports pull requests, issues, repository management,
// and a generic API escape hatch.
//
// Authentication:
//   - GITHUB_TOKEN or GH_TOKEN env var (direct PAT)
//   - Coder external auth (auto-detected via CODER_AGENT_TOKEN + CODER_AGENT_URL)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"
)

type githubPlugin struct {
	client *client
}

type githubInput struct {
	Command string `json:"command" jsonschema:"required,enum=pr_list,enum=pr_create,enum=pr_get,enum=pr_merge,enum=pr_close,enum=pr_comment,enum=issue_list,enum=issue_create,enum=issue_get,enum=issue_comment,enum=issue_close,enum=repo_get,enum=list_branches,enum=create_release,enum=api" jsonschema_description:"GitHub command to execute"`

	// Common fields
	Owner string `json:"owner,omitempty" jsonschema_description:"Repository owner (user or org). Defaults to 'codecuttle' if not specified."`
	Repo  string `json:"repo,omitempty" jsonschema_description:"Repository name. Defaults to 'codecuttlectl' if not specified."`

	// PR fields
	Title  string        `json:"title,omitempty" jsonschema_description:"PR/issue title"`
	Body   string        `json:"body,omitempty" jsonschema_description:"PR/issue body (markdown)"`
	Head   string        `json:"head,omitempty" jsonschema_description:"PR head branch (source)"`
	Base   string        `json:"base,omitempty" jsonschema_description:"PR base branch (target, default: main)"`
	Draft  bool          `json:"draft,omitempty" jsonschema_description:"Create PR as draft"`
	Number types.FlexInt `json:"number,omitempty" jsonschema_description:"PR or issue number"`

	// PR merge
	MergeMethod   string `json:"merge_method,omitempty" jsonschema:"enum=merge,enum=squash,enum=rebase" jsonschema_description:"Merge method: merge, squash, or rebase (default: merge)"`
	CommitTitle   string `json:"commit_title,omitempty" jsonschema_description:"Custom merge commit title"`
	CommitMessage string `json:"commit_message,omitempty" jsonschema_description:"Custom merge commit message"`

	// Issue fields
	Labels    []string `json:"labels,omitempty" jsonschema_description:"Labels to apply (issue_create)"`
	Assignees []string `json:"assignees,omitempty" jsonschema_description:"Users to assign (issue_create)"`
	State     string   `json:"state,omitempty" jsonschema:"enum=open,enum=closed,enum=all" jsonschema_description:"Filter by state (default: open)"`

	// Comment
	Comment string `json:"comment,omitempty" jsonschema_description:"Comment body text (markdown)"`

	// Release
	Tag         string `json:"tag,omitempty" jsonschema_description:"Tag name for release"`
	ReleaseName string `json:"release_name,omitempty" jsonschema_description:"Release title"`
	Prerelease  bool   `json:"prerelease,omitempty" jsonschema_description:"Mark as prerelease"`

	// Generic API
	Method  string          `json:"method,omitempty" jsonschema:"enum=GET,enum=POST,enum=PUT,enum=PATCH,enum=DELETE" jsonschema_description:"HTTP method for 'api' command"`
	Path    string          `json:"path,omitempty" jsonschema_description:"API path for 'api' command (e.g. /repos/owner/repo/topics)"`
	APIBody json.RawMessage `json:"api_body,omitempty" jsonschema_description:"JSON body for 'api' command"`
}

func (p *githubPlugin) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "github",
		Description: "Interact with GitHub via the REST API. Manage pull requests, issues, repositories, and releases. Supports any GitHub API endpoint via the 'api' command.",
		InputSchema: schema.MustSchema(&githubInput{}),
		LlmContextHint: `Use github for repository operations: creating PRs, listing issues, merging PRs, creating releases.

Common workflows:
- Create a PR: github(command="pr_create", head="my-branch", base="main", title="...", body="...")
- List open PRs: github(command="pr_list")
- Merge a PR: github(command="pr_merge", number=5, merge_method="squash")
- Create an issue: github(command="issue_create", title="...", body="...", labels=["bug"])
- Raw API call: github(command="api", method="GET", path="/repos/codecuttle/codecuttlectl/topics")

Owner defaults to "codecuttle" and repo defaults to "codecuttlectl" when not specified.

CRITICAL: PR and issue body fields must contain real markdown with actual newline characters.
Never use literal backslash-n (\n) escape sequences in body text — they render as visible
"\n" on GitHub instead of line breaks. When using the "api" command with api_body to update
a PR body, ensure the JSON string value contains real newlines (use \n in the JSON encoding,
which produces actual newline characters in the decoded string).`,
		Version: "1.0.0",
		CommandPatterns: []string{
			"gh *",
			"*api.github.com*",
		},
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    30,
		},
	}, nil
}

func (p *githubPlugin) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params githubInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	// Apply defaults
	if params.Owner == "" {
		params.Owner = "codecuttle"
	}
	if params.Repo == "" {
		params.Repo = "codecuttlectl"
	}

	var result string
	var execErr error

	switch params.Command {
	case "pr_list":
		result, execErr = p.prList(params)
	case "pr_create":
		result, execErr = p.prCreate(params)
	case "pr_get":
		result, execErr = p.prGet(params)
	case "pr_merge":
		result, execErr = p.prMerge(params)
	case "pr_close":
		result, execErr = p.prClose(params)
	case "pr_comment":
		result, execErr = p.prComment(params)
	case "issue_list":
		result, execErr = p.issueList(params)
	case "issue_create":
		result, execErr = p.issueCreate(params)
	case "issue_get":
		result, execErr = p.issueGet(params)
	case "issue_comment":
		result, execErr = p.issueComment(params)
	case "issue_close":
		result, execErr = p.issueClose(params)
	case "repo_get":
		result, execErr = p.repoGet(params)
	case "list_branches":
		result, execErr = p.listBranches(params)
	case "create_release":
		result, execErr = p.createRelease(params)
	case "api":
		result, execErr = p.rawAPI(params)
	default:
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("unknown command: %s", params.Command),
		}, nil
	}

	if execErr != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: execErr.Error(),
		}, nil
	}

	return &pb.ExecuteResponse{
		Output: result,
		Metadata: map[string]string{
			"command": params.Command,
			"owner":   params.Owner,
			"repo":    params.Repo,
		},
	}, nil
}

// --- Pull Requests ---

func (p *githubPlugin) prList(params githubInput) (string, error) {
	state := params.State
	if state == "" {
		state = "open"
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=%s&per_page=30", params.Owner, params.Repo, state)

	status, body, err := p.client.do("GET", path, nil)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var prs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		Head   struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Draft     bool   `json:"draft"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(body, &prs); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if len(prs) == 0 {
		return fmt.Sprintf("No %s pull requests found.", state), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d %s pull request(s):\n\n", len(prs), state))
	for _, pr := range prs {
		draft := ""
		if pr.Draft {
			draft = " [DRAFT]"
		}
		sb.WriteString(fmt.Sprintf("#%d %s%s\n  %s → %s (by @%s, %s)\n\n",
			pr.Number, pr.Title, draft, pr.Head.Ref, pr.Base.Ref, pr.User.Login, pr.CreatedAt[:10]))
	}
	return sb.String(), nil
}

func (p *githubPlugin) prCreate(params githubInput) (string, error) {
	if params.Title == "" {
		return "", fmt.Errorf("title is required for pr_create")
	}
	if params.Head == "" {
		return "", fmt.Errorf("head branch is required for pr_create")
	}
	base := params.Base
	if base == "" {
		base = "main"
	}

	reqBody := map[string]interface{}{
		"title": params.Title,
		"head":  params.Head,
		"base":  base,
	}
	if params.Body != "" {
		reqBody["body"] = params.Body
	}
	if params.Draft {
		reqBody["draft"] = true
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls", params.Owner, params.Repo)
	status, body, err := p.client.do("POST", path, reqBody)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var pr struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	json.Unmarshal(body, &pr)

	return fmt.Sprintf("Pull request #%d created: %s", pr.Number, pr.HTMLURL), nil
}

func (p *githubPlugin) prGet(params githubInput) (string, error) {
	num := params.Number.Int()
	if num == 0 {
		return "", fmt.Errorf("number is required for pr_get")
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", params.Owner, params.Repo, num)
	status, body, err := p.client.do("GET", path, nil)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var pr struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		State     string `json:"state"`
		Body      string `json:"body"`
		HTMLURL   string `json:"html_url"`
		Mergeable *bool  `json:"mergeable"`
		Draft     bool   `json:"draft"`
		Head      struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		ChangedFiles int    `json:"changed_files"`
		CreatedAt    string `json:"created_at"`
		UpdatedAt    string `json:"updated_at"`
	}
	json.Unmarshal(body, &pr)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# PR #%d: %s\n\n", pr.Number, pr.Title))
	sb.WriteString(fmt.Sprintf("**State:** %s", pr.State))
	if pr.Draft {
		sb.WriteString(" (draft)")
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("**Branch:** %s → %s\n", pr.Head.Ref, pr.Base.Ref))
	sb.WriteString(fmt.Sprintf("**Author:** @%s\n", pr.User.Login))
	sb.WriteString(fmt.Sprintf("**Changes:** +%d -%d across %d files\n", pr.Additions, pr.Deletions, pr.ChangedFiles))
	if pr.Mergeable != nil {
		sb.WriteString(fmt.Sprintf("**Mergeable:** %v\n", *pr.Mergeable))
	}
	sb.WriteString(fmt.Sprintf("**URL:** %s\n", pr.HTMLURL))
	sb.WriteString(fmt.Sprintf("**Created:** %s | **Updated:** %s\n", pr.CreatedAt[:10], pr.UpdatedAt[:10]))
	if pr.Body != "" {
		sb.WriteString(fmt.Sprintf("\n---\n\n%s\n", pr.Body))
	}

	return sb.String(), nil
}

func (p *githubPlugin) prMerge(params githubInput) (string, error) {
	num := params.Number.Int()
	if num == 0 {
		return "", fmt.Errorf("number is required for pr_merge")
	}

	method := params.MergeMethod
	if method == "" {
		method = "merge"
	}

	reqBody := map[string]interface{}{
		"merge_method": method,
	}
	if params.CommitTitle != "" {
		reqBody["commit_title"] = params.CommitTitle
	}
	if params.CommitMessage != "" {
		reqBody["commit_message"] = params.CommitMessage
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", params.Owner, params.Repo, num)
	status, body, err := p.client.do("PUT", path, reqBody)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var result struct {
		Merged  bool   `json:"merged"`
		Message string `json:"message"`
		SHA     string `json:"sha"`
	}
	json.Unmarshal(body, &result)

	if result.Merged {
		return fmt.Sprintf("PR #%d merged successfully (SHA: %s)", num, result.SHA[:8]), nil
	}
	return fmt.Sprintf("Merge failed: %s", result.Message), nil
}

func (p *githubPlugin) prClose(params githubInput) (string, error) {
	num := params.Number.Int()
	if num == 0 {
		return "", fmt.Errorf("number is required for pr_close")
	}

	reqBody := map[string]interface{}{"state": "closed"}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", params.Owner, params.Repo, num)
	status, body, err := p.client.do("PATCH", path, reqBody)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	return fmt.Sprintf("PR #%d closed.", num), nil
}

func (p *githubPlugin) prComment(params githubInput) (string, error) {
	num := params.Number.Int()
	if num == 0 {
		return "", fmt.Errorf("number is required for pr_comment")
	}
	if params.Comment == "" {
		return "", fmt.Errorf("comment is required for pr_comment")
	}

	// PR comments use the issues endpoint
	reqBody := map[string]interface{}{"body": params.Comment}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", params.Owner, params.Repo, num)
	status, body, err := p.client.do("POST", path, reqBody)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var comment struct {
		HTMLURL string `json:"html_url"`
	}
	json.Unmarshal(body, &comment)

	return fmt.Sprintf("Comment posted on PR #%d: %s", num, comment.HTMLURL), nil
}

// --- Issues ---

func (p *githubPlugin) issueList(params githubInput) (string, error) {
	state := params.State
	if state == "" {
		state = "open"
	}
	path := fmt.Sprintf("/repos/%s/%s/issues?state=%s&per_page=30", params.Owner, params.Repo, state)

	status, body, err := p.client.do("GET", path, nil)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var issues []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		PullRequest *struct{} `json:"pull_request"`
		CreatedAt   string    `json:"created_at"`
	}
	if err := json.Unmarshal(body, &issues); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	// Filter out PRs (issues endpoint returns both)
	var sb strings.Builder
	count := 0
	for _, issue := range issues {
		if issue.PullRequest != nil {
			continue
		}
		count++
		labels := ""
		if len(issue.Labels) > 0 {
			var names []string
			for _, l := range issue.Labels {
				names = append(names, l.Name)
			}
			labels = " [" + strings.Join(names, ", ") + "]"
		}
		sb.WriteString(fmt.Sprintf("#%d %s%s\n  by @%s, %s\n\n",
			issue.Number, issue.Title, labels, issue.User.Login, issue.CreatedAt[:10]))
	}

	if count == 0 {
		return fmt.Sprintf("No %s issues found.", state), nil
	}
	return fmt.Sprintf("%d %s issue(s):\n\n%s", count, state, sb.String()), nil
}

func (p *githubPlugin) issueCreate(params githubInput) (string, error) {
	if params.Title == "" {
		return "", fmt.Errorf("title is required for issue_create")
	}

	reqBody := map[string]interface{}{
		"title": params.Title,
	}
	if params.Body != "" {
		reqBody["body"] = params.Body
	}
	if len(params.Labels) > 0 {
		reqBody["labels"] = params.Labels
	}
	if len(params.Assignees) > 0 {
		reqBody["assignees"] = params.Assignees
	}

	path := fmt.Sprintf("/repos/%s/%s/issues", params.Owner, params.Repo)
	status, body, err := p.client.do("POST", path, reqBody)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var issue struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	json.Unmarshal(body, &issue)

	return fmt.Sprintf("Issue #%d created: %s", issue.Number, issue.HTMLURL), nil
}

func (p *githubPlugin) issueGet(params githubInput) (string, error) {
	num := params.Number.Int()
	if num == 0 {
		return "", fmt.Errorf("number is required for issue_get")
	}

	path := fmt.Sprintf("/repos/%s/%s/issues/%d", params.Owner, params.Repo, num)
	status, body, err := p.client.do("GET", path, nil)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var issue struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		State   string `json:"state"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Labels  []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Comments  int    `json:"comments"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	json.Unmarshal(body, &issue)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Issue #%d: %s\n\n", issue.Number, issue.Title))
	sb.WriteString(fmt.Sprintf("**State:** %s\n", issue.State))
	sb.WriteString(fmt.Sprintf("**Author:** @%s\n", issue.User.Login))
	if len(issue.Labels) > 0 {
		var names []string
		for _, l := range issue.Labels {
			names = append(names, l.Name)
		}
		sb.WriteString(fmt.Sprintf("**Labels:** %s\n", strings.Join(names, ", ")))
	}
	if len(issue.Assignees) > 0 {
		var names []string
		for _, a := range issue.Assignees {
			names = append(names, "@"+a.Login)
		}
		sb.WriteString(fmt.Sprintf("**Assignees:** %s\n", strings.Join(names, ", ")))
	}
	sb.WriteString(fmt.Sprintf("**Comments:** %d\n", issue.Comments))
	sb.WriteString(fmt.Sprintf("**URL:** %s\n", issue.HTMLURL))
	sb.WriteString(fmt.Sprintf("**Created:** %s | **Updated:** %s\n", issue.CreatedAt[:10], issue.UpdatedAt[:10]))
	if issue.Body != "" {
		sb.WriteString(fmt.Sprintf("\n---\n\n%s\n", issue.Body))
	}

	return sb.String(), nil
}

func (p *githubPlugin) issueComment(params githubInput) (string, error) {
	num := params.Number.Int()
	if num == 0 {
		return "", fmt.Errorf("number is required for issue_comment")
	}
	if params.Comment == "" {
		return "", fmt.Errorf("comment is required for issue_comment")
	}

	reqBody := map[string]interface{}{"body": params.Comment}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", params.Owner, params.Repo, num)
	status, body, err := p.client.do("POST", path, reqBody)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var comment struct {
		HTMLURL string `json:"html_url"`
	}
	json.Unmarshal(body, &comment)

	return fmt.Sprintf("Comment posted on issue #%d: %s", num, comment.HTMLURL), nil
}

func (p *githubPlugin) issueClose(params githubInput) (string, error) {
	num := params.Number.Int()
	if num == 0 {
		return "", fmt.Errorf("number is required for issue_close")
	}

	reqBody := map[string]interface{}{"state": "closed"}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", params.Owner, params.Repo, num)
	status, body, err := p.client.do("PATCH", path, reqBody)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	return fmt.Sprintf("Issue #%d closed.", num), nil
}

// --- Repository ---

func (p *githubPlugin) repoGet(params githubInput) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s", params.Owner, params.Repo)
	status, body, err := p.client.do("GET", path, nil)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var repo struct {
		FullName      string   `json:"full_name"`
		Description   string   `json:"description"`
		Private       bool     `json:"private"`
		DefaultBranch string   `json:"default_branch"`
		Language      string   `json:"language"`
		StarCount     int      `json:"stargazers_count"`
		ForksCount    int      `json:"forks_count"`
		OpenIssues    int      `json:"open_issues_count"`
		HTMLURL       string   `json:"html_url"`
		CreatedAt     string   `json:"created_at"`
		PushedAt      string   `json:"pushed_at"`
		Topics        []string `json:"topics"`
	}
	json.Unmarshal(body, &repo)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", repo.FullName))
	if repo.Description != "" {
		sb.WriteString(fmt.Sprintf("%s\n\n", repo.Description))
	}
	visibility := "public"
	if repo.Private {
		visibility = "private"
	}
	sb.WriteString(fmt.Sprintf("**Visibility:** %s\n", visibility))
	sb.WriteString(fmt.Sprintf("**Default branch:** %s\n", repo.DefaultBranch))
	sb.WriteString(fmt.Sprintf("**Language:** %s\n", repo.Language))
	sb.WriteString(fmt.Sprintf("**Stars:** %d | **Forks:** %d | **Open issues:** %d\n", repo.StarCount, repo.ForksCount, repo.OpenIssues))
	if len(repo.Topics) > 0 {
		sb.WriteString(fmt.Sprintf("**Topics:** %s\n", strings.Join(repo.Topics, ", ")))
	}
	sb.WriteString(fmt.Sprintf("**URL:** %s\n", repo.HTMLURL))
	sb.WriteString(fmt.Sprintf("**Created:** %s | **Last push:** %s\n", repo.CreatedAt[:10], repo.PushedAt[:10]))

	return sb.String(), nil
}

func (p *githubPlugin) listBranches(params githubInput) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/branches?per_page=100", params.Owner, params.Repo)
	status, body, err := p.client.do("GET", path, nil)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var branches []struct {
		Name      string `json:"name"`
		Protected bool   `json:"protected"`
	}
	json.Unmarshal(body, &branches)

	if len(branches) == 0 {
		return "No branches found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d branches:\n\n", len(branches)))
	for _, b := range branches {
		prot := ""
		if b.Protected {
			prot = " (protected)"
		}
		sb.WriteString(fmt.Sprintf("- %s%s\n", b.Name, prot))
	}
	return sb.String(), nil
}

func (p *githubPlugin) createRelease(params githubInput) (string, error) {
	if params.Tag == "" {
		return "", fmt.Errorf("tag is required for create_release")
	}

	reqBody := map[string]interface{}{
		"tag_name": params.Tag,
	}
	if params.ReleaseName != "" {
		reqBody["name"] = params.ReleaseName
	}
	if params.Body != "" {
		reqBody["body"] = params.Body
	}
	if params.Prerelease {
		reqBody["prerelease"] = true
	}

	path := fmt.Sprintf("/repos/%s/%s/releases", params.Owner, params.Repo)
	status, body, err := p.client.do("POST", path, reqBody)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("GitHub API %d: %s", status, string(body))
	}

	var release struct {
		HTMLURL string `json:"html_url"`
		TagName string `json:"tag_name"`
	}
	json.Unmarshal(body, &release)

	return fmt.Sprintf("Release %s created: %s", release.TagName, release.HTMLURL), nil
}

// --- Generic API ---

func (p *githubPlugin) rawAPI(params githubInput) (string, error) {
	if params.Path == "" {
		return "", fmt.Errorf("path is required for api command")
	}
	method := params.Method
	if method == "" {
		method = "GET"
	}

	var reqBody interface{}
	if len(params.APIBody) > 0 && string(params.APIBody) != "null" {
		var parsed interface{}
		if err := json.Unmarshal(params.APIBody, &parsed); err != nil {
			return "", fmt.Errorf("invalid api_body JSON: %w", err)
		}
		reqBody = parsed
	}

	status, body, err := p.client.do(method, params.Path, reqBody)
	if err != nil {
		return "", err
	}

	// Pretty-print JSON response
	var pretty json.RawMessage
	if err := json.Unmarshal(body, &pretty); err == nil {
		formatted, fmtErr := json.MarshalIndent(pretty, "", "  ")
		if fmtErr == nil {
			return fmt.Sprintf("HTTP %d\n\n%s", status, string(formatted)), nil
		}
	}

	return fmt.Sprintf("HTTP %d\n\n%s", status, string(body)), nil
}

func main() {
	plugin := &githubPlugin{
		client: newClient(),
	}
	pluginkit.Serve(plugin)
}
