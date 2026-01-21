package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/benjamincozon/feedenrich/internal/config"
	"golang.org/x/net/html"
)

// WebSearchTool searches the web for information
type WebSearchTool struct {
	config *config.Config
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string {
	return "Search the web for factual information about the product (specifications, official details)"
}

func (t *WebSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"site": map[string]any{
				"type":        "string",
				"description": "Limit search to a specific site (e.g., 'nike.com')",
			},
			"num_results": map[string]any{
				"type":        "integer",
				"description": "Number of results to return (default 5)",
			},
		},
		"required": []string{"query"},
	}
}

type WebSearchInput struct {
	Query      string `json:"query"`
	Site       string `json:"site,omitempty"`
	NumResults int    `json:"num_results,omitempty"`
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type WebSearchOutput struct {
	Results []SearchResult `json:"results"`
}

func (t *WebSearchTool) Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error) {
	var params WebSearchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	if !t.config.Agent.EnableWebSearch {
		return WebSearchOutput{Results: []SearchResult{}}, nil
	}

	query := params.Query
	if params.Site != "" {
		query = fmt.Sprintf("site:%s %s", params.Site, query)
	}

	numResults := params.NumResults
	if numResults == 0 {
		numResults = 5
	}

	// Use Serper API for search
	results, err := t.searchWithSerper(ctx, query, numResults)
	if err != nil {
		return nil, err
	}

	return WebSearchOutput{Results: results}, nil
}

func (t *WebSearchTool) searchWithSerper(ctx context.Context, query string, numResults int) ([]SearchResult, error) {
	if t.config.WebSearch.APIKey == "" {
		// Fallback: return empty results if no API key
		return []SearchResult{}, nil
	}

	reqBody := fmt.Sprintf(`{"q": %q, "num": %d}`, query, numResults)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://google.serper.dev/search", strings.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-API-KEY", t.config.WebSearch.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serper request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("serper error %d: %s", resp.StatusCode, string(body))
	}

	var serperResp struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&serperResp); err != nil {
		return nil, fmt.Errorf("parse serper response: %w", err)
	}

	var results []SearchResult
	for _, r := range serperResp.Organic {
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.Link,
			Snippet: r.Snippet,
		})
	}

	return results, nil
}

// FetchPageTool fetches and extracts content from a web page
type FetchPageTool struct{}

func (t *FetchPageTool) Name() string { return "fetch_page" }

func (t *FetchPageTool) Description() string {
	return "Fetch a web page and extract its text content for detailed information"
}

func (t *FetchPageTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "URL of the page to fetch",
			},
		},
		"required": []string{"url"},
	}
}

type FetchPageInput struct {
	URL string `json:"url"`
}

type FetchPageOutput struct {
	Title          string         `json:"title"`
	Content        string         `json:"content"`
	StructuredData map[string]any `json:"structured_data,omitempty"`
	FetchedAt      time.Time      `json:"fetched_at"`
	Error          string         `json:"error,omitempty"`
}

func (t *FetchPageTool) Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error) {
	var params FetchPageInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	// Validate URL
	parsedURL, err := url.Parse(params.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return FetchPageOutput{Error: "Invalid URL"}, nil
	}

	// Fetch the page
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", params.URL, nil)
	if err != nil {
		return FetchPageOutput{Error: err.Error()}, nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FeedEnrichBot/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return FetchPageOutput{Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return FetchPageOutput{Error: fmt.Sprintf("HTTP %d", resp.StatusCode)}, nil
	}

	// Parse HTML
	doc, err := html.Parse(resp.Body)
	if err != nil {
		return FetchPageOutput{Error: "Failed to parse HTML"}, nil
	}

	// Extract title and content
	title := extractTitle(doc)
	content := extractTextContent(doc, 5000) // Limit to 5000 chars

	return FetchPageOutput{
		Title:     title,
		Content:   content,
		FetchedAt: time.Now(),
	}, nil
}

func extractTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		if n.FirstChild != nil {
			return n.FirstChild.Data
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if title := extractTitle(c); title != "" {
			return title
		}
	}
	return ""
}

func extractTextContent(n *html.Node, maxLen int) string {
	var sb strings.Builder
	extractText(n, &sb, maxLen)
	return strings.TrimSpace(sb.String())
}

func extractText(n *html.Node, sb *strings.Builder, maxLen int) {
	if sb.Len() >= maxLen {
		return
	}

	// Skip script, style, nav, footer
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "nav", "footer", "header", "aside":
			return
		}
	}

	if n.Type == html.TextNode {
		text := strings.TrimSpace(n.Data)
		if text != "" {
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(text)
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractText(c, sb, maxLen)
	}
}
