// cuttlebone-web-fetch is a Cuttlebone plugin that fetches URL content and
// converts it to readable text or markdown. It handles HTML pages by stripping
// scripts/styles and extracting meaningful text content.
//
// This is the "I have a URL, give me its content" tool — complementary to
// websearch which finds URLs. Together they form the web access layer.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"
)

type webFetchTool struct {
	httpClient *http.Client
}

type webFetchInput struct {
	URL     string        `json:"url" jsonschema:"required" jsonschema_description:"The URL to fetch content from. Must start with http:// or https://"`
	Format  string        `json:"format,omitempty" jsonschema:"enum=text,enum=markdown,enum=html" jsonschema_description:"Output format: 'text' (default, clean readable text), 'markdown' (preserve structure), 'html' (raw HTML)"`
	MaxSize types.FlexInt `json:"max_size,omitempty" jsonschema_description:"Maximum content length in characters to return (default: 50000, max: 200000)"`
}

func (t *webFetchTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "webfetch",
		Description: "Fetch content from a URL and return it as readable text. Converts HTML pages to clean text by stripping scripts, styles, and navigation. Use for reading documentation, GitHub READMEs, blog posts, or any web page.",
		InputSchema: schema.MustSchema(&webFetchInput{}),
		LlmContextHint: `Use webfetch to retrieve content from specific URLs — documentation pages, GitHub files, blog posts, API references. The URL must be fully-qualified (https://...). For finding URLs to fetch, use websearch first.

Prefer format "text" for general reading. Use "markdown" when you need to preserve headings and code blocks. Use "html" only when you need raw markup.`,
		Version: "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    30,
		},
	}, nil
}

func (t *webFetchTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params webFetchInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.URL == "" {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: "url is required",
		}, nil
	}

	if !strings.HasPrefix(params.URL, "http://") && !strings.HasPrefix(params.URL, "https://") {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: "url must start with http:// or https://",
		}, nil
	}

	format := params.Format
	if format == "" {
		format = "text"
	}

	maxSize := params.MaxSize.Int()
	if maxSize <= 0 {
		maxSize = 50000
	}
	if maxSize > 200000 {
		maxSize = 200000
	}

	content, contentType, err := t.fetch(ctx, params.URL)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("fetch failed: %v", err),
		}, nil
	}

	// Convert based on format and content type
	var output string
	switch format {
	case "html":
		output = content
	case "markdown":
		if isHTML(contentType) {
			output = htmlToMarkdown(content)
		} else {
			output = content
		}
	default: // "text"
		if isHTML(contentType) {
			output = htmlToText(content)
		} else {
			output = content
		}
	}

	// Truncate if needed
	if len(output) > maxSize {
		output = output[:maxSize] + fmt.Sprintf("\n\n(content truncated at %d characters)", maxSize)
	}

	return &pb.ExecuteResponse{
		Output: output,
		Metadata: map[string]string{
			"url":          params.URL,
			"content_type": contentType,
			"format":       format,
			"length":       fmt.Sprintf("%d", len(output)),
		},
	}, nil
}

func (t *webFetchTool) fetch(ctx context.Context, url string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", fmt.Errorf("building request: %w", err)
	}

	// Use a browser-like user agent to avoid bot detection
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.7")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Limit read to 5MB to prevent memory issues
	const maxBytes = 5 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return "", "", fmt.Errorf("reading response: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	return string(body), contentType, nil
}

func isHTML(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")
}

// htmlToText extracts readable text from HTML by stripping all tags and
// non-content elements (scripts, styles, nav). Preserves paragraph structure.
func htmlToText(html string) string {
	// Remove script and style blocks entirely
	html = stripBlocks(html, "script")
	html = stripBlocks(html, "style")
	html = stripBlocks(html, "noscript")
	html = stripBlocks(html, "nav")
	html = stripBlocks(html, "header")
	html = stripBlocks(html, "footer")

	// Convert block elements to newlines
	blockTags := []string{"div", "p", "br", "h1", "h2", "h3", "h4", "h5", "h6",
		"li", "tr", "td", "th", "blockquote", "pre", "hr", "section", "article"}
	for _, tag := range blockTags {
		html = strings.ReplaceAll(html, "<"+tag, "\n<"+tag)
		html = strings.ReplaceAll(html, "</"+tag+">", "</"+tag+">\n")
	}

	// Strip remaining HTML tags
	var result strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			result.WriteRune(r)
		}
	}

	// Decode common HTML entities
	text := result.String()
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", `"`)
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&apos;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")

	// Collapse whitespace: multiple newlines → max 2, multiple spaces → 1
	lines := strings.Split(text, "\n")
	var cleaned []string
	emptyCount := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			emptyCount++
			if emptyCount <= 2 {
				cleaned = append(cleaned, "")
			}
		} else {
			emptyCount = 0
			cleaned = append(cleaned, line)
		}
	}

	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

// htmlToMarkdown does a basic HTML-to-Markdown conversion.
// Handles headings, paragraphs, links, code blocks, lists, and bold/italic.
func htmlToMarkdown(html string) string {
	// Remove non-content blocks
	html = stripBlocks(html, "script")
	html = stripBlocks(html, "style")
	html = stripBlocks(html, "noscript")

	// Convert headings
	for i := 6; i >= 1; i-- {
		prefix := strings.Repeat("#", i)
		tag := fmt.Sprintf("h%d", i)
		html = replaceTag(html, tag, "\n"+prefix+" ", "\n")
	}

	// Convert paragraphs and divs to newlines
	html = replaceTag(html, "p", "\n\n", "\n\n")
	html = replaceTag(html, "div", "\n", "\n")

	// Convert line breaks
	html = strings.ReplaceAll(html, "<br>", "\n")
	html = strings.ReplaceAll(html, "<br/>", "\n")
	html = strings.ReplaceAll(html, "<br />", "\n")

	// Convert bold/italic
	html = replaceTag(html, "strong", "**", "**")
	html = replaceTag(html, "b", "**", "**")
	html = replaceTag(html, "em", "*", "*")
	html = replaceTag(html, "i", "*", "*")

	// Convert code
	html = replaceTag(html, "code", "`", "`")
	html = replaceTag(html, "pre", "\n```\n", "\n```\n")

	// Convert list items
	html = replaceTag(html, "li", "\n- ", "")

	// Convert horizontal rules
	html = strings.ReplaceAll(html, "<hr>", "\n---\n")
	html = strings.ReplaceAll(html, "<hr/>", "\n---\n")
	html = strings.ReplaceAll(html, "<hr />", "\n---\n")

	// Strip remaining tags
	var result strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			result.WriteRune(r)
		}
	}

	text := result.String()
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", `"`)
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&apos;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")

	// Collapse excessive newlines
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(text)
}

// stripBlocks removes all instances of <tag>...</tag> including content.
func stripBlocks(html, tag string) string {
	for {
		openTag := "<" + tag
		start := strings.Index(strings.ToLower(html), openTag)
		if start == -1 {
			break
		}
		// Find the end of the opening tag (could have attributes)
		tagEnd := strings.Index(html[start:], ">")
		if tagEnd == -1 {
			break
		}

		closeTag := "</" + tag + ">"
		end := strings.Index(strings.ToLower(html[start:]), closeTag)
		if end == -1 {
			// Self-closing or malformed — just remove the opening tag
			html = html[:start] + html[start+tagEnd+1:]
			continue
		}
		html = html[:start] + html[start+end+len(closeTag):]
	}
	return html
}

// replaceTag replaces <tag>content</tag> with prefix+content+suffix.
func replaceTag(html, tag, prefix, suffix string) string {
	openTag := "<" + tag
	closeTag := "</" + tag + ">"

	for {
		lower := strings.ToLower(html)
		start := strings.Index(lower, openTag)
		if start == -1 {
			break
		}
		// Find end of opening tag
		tagEnd := strings.Index(html[start:], ">")
		if tagEnd == -1 {
			break
		}
		// Find close tag
		closeStart := strings.Index(lower[start+tagEnd+1:], closeTag)
		if closeStart == -1 {
			// No close tag — just strip the open tag
			html = html[:start] + prefix + html[start+tagEnd+1:]
			continue
		}
		closeStart += start + tagEnd + 1
		content := html[start+tagEnd+1 : closeStart]
		html = html[:start] + prefix + content + suffix + html[closeStart+len(closeTag):]
	}
	return html
}

func main() {
	tool := &webFetchTool{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
	pluginkit.Serve(tool)
}
