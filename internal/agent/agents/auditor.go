package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// ProductAuditor is a JUDGE-ONLY agent.
// It evaluates product data and outputs structured diagnostics.
// It does NO rewriting, NO suggestions, NO creativity.
// Only judgment.
type ProductAuditor struct {
	client *openai.Client
	config *config.Config
}

func NewProductAuditor(cfg *config.Config) *ProductAuditor {
	return &ProductAuditor{
		client: openai.NewClient(cfg.OpenAI.APIKey),
		config: cfg,
	}
}

// AuditInput contains all context needed for auditing
type AuditInput struct {
	ProductData json.RawMessage `json:"product_data"`
	HardRules   []HardRule      `json:"hard_rules"`
	GMCRules    []GMCRule       `json:"gmc_rules"`
}

type HardRule struct {
	Field     string `json:"field"`
	Rule      string `json:"rule"`
	Condition string `json:"condition"`
	Message   string `json:"message"`
}

type GMCRule struct {
	Field       string `json:"field"`
	Requirement string `json:"requirement"`
	Severity    string `json:"severity"` // error, warning
}

// AuditOutput is the structured diagnostic output
// NO suggestions, NO copywriting, NO creativity - ONLY judgment
type AuditOutput struct {
	Violations []Violation `json:"violations"`
	Weaknesses []Weakness  `json:"weaknesses"`
	Missing    []string    `json:"missing_required"`
	Scores     AuditScores `json:"scores"`
}

type Violation struct {
	Field    string `json:"field"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // error, warning
	Evidence string `json:"evidence"` // what was found
}

type Weakness struct {
	Field    string `json:"field"`
	Issue    string `json:"issue"`
	Severity string `json:"severity"` // low, medium, high
}

type AuditScores struct {
	GMCCompliance      float64 `json:"gmc_compliance"`       // 0-1
	DataCompleteness   float64 `json:"data_completeness"`    // 0-1
	TitleQuality       float64 `json:"title_quality"`        // 0-1
	DescriptionQuality float64 `json:"description_quality"`  // 0-1
	AgentReadiness     float64 `json:"agent_readiness_score"` // 0-1
}

// Audit evaluates product data and returns structured diagnostics
func (a *ProductAuditor) Audit(ctx context.Context, input AuditInput) (*AuditOutput, error) {
	rulesJSON, _ := json.MarshalIndent(input.HardRules, "", "  ")
	gmcRulesJSON, _ := json.MarshalIndent(input.GMCRules, "", "  ")

	prompt := fmt.Sprintf(`You are a PRODUCT AUDITOR. Your role is STRICTLY to JUDGE product data quality.

CRITICAL CONSTRAINTS:
- You do NO rewriting
- You make NO suggestions
- You show NO creativity
- You output ONLY structured diagnostics

TASK: Evaluate this product against the rules and output findings.

PRODUCT DATA:
%s

HARD RULES TO CHECK:
%s

GMC RULES TO CHECK:
%s

OUTPUT FORMAT (JSON only):
{
  "violations": [
    { "field": "...", "rule": "...", "severity": "error|warning", "evidence": "what was found" }
  ],
  "weaknesses": [
    { "field": "...", "issue": "...", "severity": "low|medium|high" }
  ],
  "missing_required": ["field1", "field2"],
  "scores": {
    "gmc_compliance": 0.0-1.0,
    "data_completeness": 0.0-1.0,
    "title_quality": 0.0-1.0,
    "description_quality": 0.0-1.0,
    "agent_readiness_score": 0.0-1.0
  }
}

SCORING GUIDELINES:
- gmc_compliance: 1.0 = no violations, 0.0 = critical violations
- data_completeness: based on required/recommended fields present
- title_quality: based on length, structure, keywords (NOT creativity)
- description_quality: based on informativeness, length, clarity
- agent_readiness_score: overall score for AI consumption

Return ONLY the JSON, no explanations.`, string(input.ProductData), string(rulesJSON), string(gmcRulesJSON))

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
		return nil, fmt.Errorf("auditor call failed: %w", err)
	}

	var output AuditOutput
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &output); err != nil {
		return nil, fmt.Errorf("parse audit output: %w", err)
	}

	return &output, nil
}
