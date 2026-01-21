package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// ControllerAgent exists ONLY to say "no".
// It compares before/after, validates facts used, checks rules.
// If something smells wrong → REJECT.
// This agent is what makes enterprises TRUST the system.
type ControllerAgent struct {
	client *openai.Client
	config *config.Config
}

func NewControllerAgent(cfg *config.Config) *ControllerAgent {
	return &ControllerAgent{
		client: openai.NewClient(cfg.OpenAI.APIKey),
		config: cfg,
	}
}

// ControllerInput contains the change to validate
type ControllerInput struct {
	Field         string            `json:"field"`
	Before        string            `json:"before"`
	After         string            `json:"after"`
	FactsUsed     []FactUsage       `json:"facts_used"`
	AllowedFacts  map[string]string `json:"allowed_facts"`
	Constraints   []string          `json:"constraints"`
	WriterConfidence float64        `json:"writer_confidence"`
}

// ControllerOutput is the validation result
type ControllerOutput struct {
	Approved    bool              `json:"approved"`
	Rejections  []Rejection       `json:"rejections,omitempty"`
	Warnings    []Warning         `json:"warnings,omitempty"`
	Verification VerificationResult `json:"verification"`
}

type Rejection struct {
	Reason   string `json:"reason"`
	Severity string `json:"severity"` // critical, major
	Evidence string `json:"evidence"` // what triggered the rejection
}

type Warning struct {
	Reason string `json:"reason"`
	Risk   string `json:"risk"`
}

type VerificationResult struct {
	FactsVerified     bool    `json:"facts_verified"`      // all facts traceable
	ConstraintsMet    bool    `json:"constraints_met"`     // all constraints followed
	NoInvention       bool    `json:"no_invention"`        // no new info invented
	MeaningPreserved  bool    `json:"meaning_preserved"`   // original meaning kept
	RulesCompliant    bool    `json:"rules_compliant"`     // GMC rules followed
	OverallConfidence float64 `json:"overall_confidence"`
}

// Validate checks a proposed change and approves or rejects it
func (c *ControllerAgent) Validate(ctx context.Context, input ControllerInput) (*ControllerOutput, error) {
	allowedJSON, _ := json.MarshalIndent(input.AllowedFacts, "", "  ")
	factsUsedJSON, _ := json.MarshalIndent(input.FactsUsed, "", "  ")
	constraintsJSON, _ := json.Marshal(input.Constraints)

	prompt := fmt.Sprintf(`You are a CONTROLLER AGENT. Your job is to VALIDATE changes and REJECT anything suspicious.

YOUR ROLE:
- Compare before/after
- Verify facts are traceable to allowed sources
- Check all constraints are met
- Detect any invention or hallucination
- REJECT if anything is wrong

VALIDATION RULES:
1. Every fact in "after" must be traceable to "allowed_facts" or "before"
2. No new information should appear that wasn't in allowed sources
3. All constraints must be satisfied
4. Original meaning must be preserved (no semantic changes)
5. GMC compliance rules must be followed

INPUT - CHANGE TO VALIDATE:
- Field: %s
- Before: %s
- After: %s
- Writer confidence: %.2f

FACTS CLAIMED TO BE USED:
%s

ALLOWED FACTS (source of truth):
%s

CONSTRAINTS TO CHECK:
%s

REJECTION TRIGGERS:
- Fact claimed but not in allowed_facts → REJECT
- New information appeared with no source → REJECT
- Constraint violated → REJECT
- Meaning changed significantly → REJECT (warn if minor)
- Promotional language added → REJECT
- Unverifiable claims → REJECT

OUTPUT FORMAT (JSON only):
{
  "approved": true/false,
  "rejections": [
    { "reason": "why rejected", "severity": "critical|major", "evidence": "what triggered it" }
  ],
  "warnings": [
    { "reason": "concern", "risk": "low|medium" }
  ],
  "verification": {
    "facts_verified": true/false,
    "constraints_met": true/false,
    "no_invention": true/false,
    "meaning_preserved": true/false,
    "rules_compliant": true/false,
    "overall_confidence": 0.0-1.0
  }
}

BE STRICT. When in doubt, REJECT.

Return ONLY the JSON, no explanations.`, input.Field, input.Before, input.After, input.WriterConfidence, string(factsUsedJSON), string(allowedJSON), string(constraintsJSON))

	resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: c.config.OpenAI.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("controller call failed: %w", err)
	}

	var output ControllerOutput
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &output); err != nil {
		return nil, fmt.Errorf("parse controller output: %w", err)
	}

	return &output, nil
}
