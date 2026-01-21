package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// OptimizationPlanner decides WHAT should be optimized, not HOW.
// This is where the current system collapses - jumping directly to rewriting.
// This agent decides:
// - what should be optimized
// - what should NOT be optimized
// - what is risky
// - what requires human validation
// NO text generation - DECISION LOGIC only
type OptimizationPlanner struct {
	client *openai.Client
	config *config.Config
}

func NewOptimizationPlanner(cfg *config.Config) *OptimizationPlanner {
	return &OptimizationPlanner{
		client: openai.NewClient(cfg.OpenAI.APIKey),
		config: cfg,
	}
}

// PlannerInput contains audit results and available evidence
type PlannerInput struct {
	ProductData     json.RawMessage `json:"product_data"`
	AuditResult     *AuditOutput    `json:"audit_result"`
	AvailableEvidence struct {
		ImageEvidence  *ImageEvidenceOutput `json:"image_evidence,omitempty"`
		RetrievedFacts *RetrievalOutput     `json:"retrieved_facts,omitempty"`
	} `json:"available_evidence"`
}

// PlannerOutput contains optimization decisions
// NO text generation - DECISION LOGIC only
type PlannerOutput struct {
	Actions       []OptimizationAction `json:"actions"`
	DoNotOptimize []DoNotOptimize      `json:"do_not_optimize"`
	RequireHuman  []HumanRequired      `json:"require_human"`
}

type OptimizationAction struct {
	Field         string   `json:"field"`
	Objective     string   `json:"objective"`      // e.g., "clarify product type", "add missing color"
	Risk          string   `json:"risk"`           // low, medium, high
	AllowedFacts  []string `json:"allowed_facts"`  // fields that CAN be used
	ForbiddenFacts []string `json:"forbidden_facts"` // fields that MUST NOT be used
	Constraints   []string `json:"constraints"`    // e.g., "max 150 chars", "no promo text"
	Priority      int      `json:"priority"`       // 1 = highest
}

type DoNotOptimize struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type HumanRequired struct {
	Field     string `json:"field"`
	Reason    string `json:"reason"`
	RiskLevel string `json:"risk_level"`
}

// Plan creates an optimization plan based on audit results and evidence
func (p *OptimizationPlanner) Plan(ctx context.Context, input PlannerInput) (*PlannerOutput, error) {
	auditJSON, _ := json.MarshalIndent(input.AuditResult, "", "  ")

	evidenceJSON := []byte("{}")
	if input.AvailableEvidence.ImageEvidence != nil || input.AvailableEvidence.RetrievedFacts != nil {
		evidenceJSON, _ = json.MarshalIndent(input.AvailableEvidence, "", "  ")
	}

	prompt := fmt.Sprintf(`You are an OPTIMIZATION PLANNER. You decide WHAT should be optimized, NOT how to write it.

CRITICAL CONSTRAINTS:
- You do NO text generation
- You only make DECISIONS about what to optimize
- You classify RISK for each action
- You identify what requires HUMAN validation

RISK CLASSIFICATION:
- LOW: Formatting fixes, case corrections, adding data from verified sources
- MEDIUM: Restructuring content, adding data from images, rewording
- HIGH: Technical specifications, health/safety claims, compatibility → REQUIRE HUMAN

INPUT - PRODUCT DATA:
%s

INPUT - AUDIT RESULT:
%s

INPUT - AVAILABLE EVIDENCE:
%s

DECISION RULES:
1. If audit shows violation → plan fix with appropriate risk level
2. If audit shows weakness → plan improvement ONLY if evidence supports it
3. If no evidence available for a field → DO NOT OPTIMIZE or REQUIRE HUMAN
4. If change could affect safety/compliance → REQUIRE HUMAN

OUTPUT FORMAT (JSON only):
{
  "actions": [
    {
      "field": "title",
      "objective": "clarify product type",
      "risk": "low",
      "allowed_facts": ["brand", "product_type", "color"],
      "forbidden_facts": ["performance_claims", "unverified_specs"],
      "constraints": ["max 150 chars", "no promo text", "front-load keywords"],
      "priority": 1
    }
  ],
  "do_not_optimize": [
    { "field": "price", "reason": "no issues found" }
  ],
  "require_human": [
    { "field": "material", "reason": "cannot verify from available sources", "risk_level": "high" }
  ]
}

Return ONLY the JSON, no explanations.`, string(input.ProductData), string(auditJSON), evidenceJSON)

	resp, err := p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: p.config.OpenAI.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("planner call failed: %w", err)
	}

	var output PlannerOutput
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &output); err != nil {
		return nil, fmt.Errorf("parse planner output: %w", err)
	}

	return &output, nil
}
