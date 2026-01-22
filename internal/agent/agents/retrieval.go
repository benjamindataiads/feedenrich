package agents

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
	openai "github.com/sashabaranov/go-openai"
)

// KnowledgeRetrievalAgent fetches facts from external sources.
// This is how we ELIMINATE hallucination.
// Every fact must have a verifiable source.
type KnowledgeRetrievalAgent struct {
	client     *openai.Client
	httpClient *http.Client
	config     *config.Config
}

func NewKnowledgeRetrievalAgent(cfg *config.Config) *KnowledgeRetrievalAgent {
	return &KnowledgeRetrievalAgent{
		client: openai.NewClient(cfg.OpenAI.APIKey),
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		config: cfg,
	}
}

// RetrievalInput specifies what facts to search for
type RetrievalInput struct {
	ProductTitle string   `json:"product_title"`
	Brand        string   `json:"brand,omitempty"`
	GTIN         string   `json:"gtin,omitempty"`
	MPN          string   `json:"mpn,omitempty"`
	ProductURL   string   `json:"product_url,omitempty"`
	FieldsNeeded []string `json:"fields_needed"` // e.g., ["material", "dimensions", "weight"]
}

// RetrievalOutput contains sourced facts
type RetrievalOutput struct {
	Facts         []SourcedFact `json:"facts"`
	SourcesUsed   []Source      `json:"sources_used"`
	FieldsNotFound []string     `json:"fields_not_found"`
}

type SourcedFact struct {
	Field      string  `json:"field"`
	Value      string  `json:"value"`
	Source     string  `json:"source"`      // "manufacturer_page", "product_page", "feed"
	URL        string  `json:"url"`         // verifiable URL
	Evidence   string  `json:"evidence"`    // exact text snippet from source
	Confidence float64 `json:"confidence"`
}

type Source struct {
	Type string `json:"type"` // "product_page", "manufacturer", "web_search"
	URL  string `json:"url"`
	Used bool   `json:"used"`
}

// RetrieveFacts searches for verifiable facts about a product
func (a *KnowledgeRetrievalAgent) RetrieveFacts(ctx context.Context, input RetrievalInput) (*RetrievalOutput, error) {
	output := &RetrievalOutput{
		Facts:         []SourcedFact{},
		SourcesUsed:   []Source{},
		FieldsNotFound: []string{},
	}

	// 1. Try product URL first if available
	if input.ProductURL != "" {
		pageContent, err := a.fetchPage(ctx, input.ProductURL)
		if err == nil {
			facts, err := a.extractFactsFromPage(ctx, pageContent, input.FieldsNeeded, input.ProductURL)
			if err == nil {
				output.Facts = append(output.Facts, facts...)
				output.SourcesUsed = append(output.SourcesUsed, Source{
					Type: "product_page",
					URL:  input.ProductURL,
					Used: true,
				})
			}
		}
	}

	// 2. Build search queries for missing fields
	foundFields := make(map[string]bool)
	for _, f := range output.Facts {
		foundFields[f.Field] = true
	}

	var missingFields []string
	for _, field := range input.FieldsNeeded {
		if !foundFields[field] {
			missingFields = append(missingFields, field)
		}
	}

	// 3. Search for remaining fields if we have product identifiers
	if len(missingFields) > 0 && (input.GTIN != "" || input.Brand != "") {
		searchQuery := a.buildSearchQuery(input, missingFields)
		searchResults, err := a.webSearch(ctx, searchQuery)
		if err == nil && len(searchResults) > 0 {
			// Try to fetch and extract from top results
			for _, result := range searchResults[:min(3, len(searchResults))] {
				pageContent, err := a.fetchPage(ctx, result.URL)
				if err != nil {
					continue
				}

				facts, err := a.extractFactsFromPage(ctx, pageContent, missingFields, result.URL)
				if err != nil {
					continue
				}

				output.Facts = append(output.Facts, facts...)
				output.SourcesUsed = append(output.SourcesUsed, Source{
					Type: "web_search",
					URL:  result.URL,
					Used: len(facts) > 0,
				})

				// Update found fields
				for _, f := range facts {
					foundFields[f.Field] = true
				}
			}
		}
	}

	// Record fields that couldn't be found
	for _, field := range input.FieldsNeeded {
		if !foundFields[field] {
			output.FieldsNotFound = append(output.FieldsNotFound, field)
		}
	}

	return output, nil
}

func (a *KnowledgeRetrievalAgent) fetchPage(ctx context.Context, pageURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FeedEnrich/1.0)")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("page returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024)) // 100KB limit
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func (a *KnowledgeRetrievalAgent) extractFactsFromPage(ctx context.Context, content string, fieldsNeeded []string, sourceURL string) ([]SourcedFact, error) {
	// Truncate content for prompt
	if len(content) > 15000 {
		content = content[:15000]
	}

	fieldsJSON, _ := json.Marshal(fieldsNeeded)

	prompt := fmt.Sprintf(`You are a FACT EXTRACTOR. Extract ONLY verifiable facts from this page content.

CRITICAL CONSTRAINTS:
- Extract ONLY facts that are EXPLICITLY stated in the content
- NO inference, NO assumptions
- Include the EXACT text snippet as evidence
- If a field is not found, do NOT include it

FIELDS TO EXTRACT: %s

PAGE CONTENT:
%s

OUTPUT FORMAT (JSON only):
{
  "facts": [
    {
      "field": "material",
      "value": "100%% cotton",
      "evidence": "Made from 100%% organic cotton",
      "confidence": 0.95
    }
  ]
}

Return ONLY the JSON with facts found. Empty array if nothing found.`, string(fieldsJSON), content)

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.config.OpenAI.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Facts []struct {
			Field      string  `json:"field"`
			Value      string  `json:"value"`
			Evidence   string  `json:"evidence"`
			Confidence float64 `json:"confidence"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, err
	}

	// Convert to SourcedFact with URL
	facts := make([]SourcedFact, 0, len(result.Facts))
	for _, f := range result.Facts {
		facts = append(facts, SourcedFact{
			Field:      f.Field,
			Value:      f.Value,
			Source:     "product_page",
			URL:        sourceURL,
			Evidence:   f.Evidence,
			Confidence: f.Confidence,
		})
	}

	return facts, nil
}

type searchResult struct {
	URL     string
	Title   string
	Snippet string
}

func (a *KnowledgeRetrievalAgent) webSearch(ctx context.Context, query string) ([]searchResult, error) {
	// Check if web search is enabled and API key is set
	if a.config.WebSearch.APIKey == "" {
		return []searchResult{}, nil
	}

	// Use Brave Search API
	searchURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=5&extra_snippets=true", 
		url.QueryEscape(query))
	
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Subscription-Token", a.config.WebSearch.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("brave search error %d: %s", resp.StatusCode, string(body))
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				ExtraSnippets []string `json:"extra_snippets,omitempty"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		return nil, fmt.Errorf("parse brave response: %w", err)
	}

	var results []searchResult
	for _, r := range braveResp.Web.Results {
		snippet := r.Description
		// Include extra snippets for more context
		if len(r.ExtraSnippets) > 0 {
			snippet += " | " + strings.Join(r.ExtraSnippets, " | ")
		}
		results = append(results, searchResult{
			URL:     r.URL,
			Title:   r.Title,
			Snippet: snippet,
		})
	}

	return results, nil
}

// buildSearchQuery creates optimized search queries for product information
func (a *KnowledgeRetrievalAgent) buildSearchQuery(input RetrievalInput, fields []string) string {
	var queryParts []string

	// Priority 1: GTIN/EAN search (most precise identifier)
	if input.GTIN != "" {
		// Quote GTIN for exact match
		queryParts = append(queryParts, fmt.Sprintf(`"%s"`, input.GTIN))
	}

	// Priority 2: Brand + Product name
	if input.Brand != "" {
		queryParts = append(queryParts, input.Brand)
	}
	if input.ProductTitle != "" {
		// Clean title - take first meaningful part
		title := input.ProductTitle
		if len(title) > 50 {
			title = title[:50]
		}
		queryParts = append(queryParts, title)
	}

	// Priority 3: MPN if available
	if input.MPN != "" {
		queryParts = append(queryParts, input.MPN)
	}

	// Add specification keywords for the fields we need
	if len(fields) > 0 {
		queryParts = append(queryParts, "specifications")
		// Add specific field names
		for _, f := range fields {
			if f == "material" || f == "color" || f == "dimensions" || f == "weight" {
				queryParts = append(queryParts, f)
			}
		}
	}

	return strings.Join(queryParts, " ")
}

// SearchByGTIN searches specifically by GTIN/EAN for high-confidence results
func (a *KnowledgeRetrievalAgent) SearchByGTIN(ctx context.Context, gtin string) ([]searchResult, error) {
	// GTIN search with exact match - high confidence source
	query := fmt.Sprintf(`"%s" product`, gtin)
	return a.webSearch(ctx, query)
}

// SearchByBrandProduct searches by brand and product name
func (a *KnowledgeRetrievalAgent) SearchByBrandProduct(ctx context.Context, brand, product string) ([]searchResult, error) {
	query := fmt.Sprintf(`%s "%s" specifications`, brand, product)
	return a.webSearch(ctx, query)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
