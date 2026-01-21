package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// ValidateProposalTool validates a proposal before committing
type ValidateProposalTool struct {
	client *openai.Client
	config *config.Config
}

func (t *ValidateProposalTool) Name() string { return "validate_proposal" }

func (t *ValidateProposalTool) Description() string {
	return "Validate a proposal for no-invention violations, source verification, and risk assessment. MUST be called before commit_changes."
}

func (t *ValidateProposalTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"field": map[string]any{
				"type":        "string",
				"description": "The field being modified",
			},
			"before": map[string]any{
				"type":        "string",
				"description": "Value before the change",
			},
			"after": map[string]any{
				"type":        "string",
				"description": "Proposed new value",
			},
			"sources": map[string]any{
				"type":        "array",
				"description": "Sources justifying the changes",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":      map[string]any{"type": "string"},
						"reference": map[string]any{"type": "string"},
						"evidence":  map[string]any{"type": "string"},
					},
				},
			},
		},
		"required": []string{"field", "before", "after", "sources"},
	}
}

type ValidateProposalInput struct {
	Field   string   `json:"field"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
	Sources []Source `json:"sources"`
}

type ValidateProposalOutput struct {
	Valid              bool   `json:"valid"`
	RiskLevel          string `json:"risk_level"` // low, medium, high
	Issues             []struct {
		Type   string `json:"type"`   // unsourced_fact, rule_violation, invention_detected
		Detail string `json:"detail"`
	} `json:"issues"`
	RequiresHumanReview bool `json:"requires_human_review"`
}

func (t *ValidateProposalTool) Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error) {
	var params ValidateProposalInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	sourcesJSON, _ := json.MarshalIndent(params.Sources, "", "  ")

	prompt := fmt.Sprintf(`Valide cette proposition de modification:

Champ: %s
Avant: %s
Après: %s

Sources fournies:
%s

VÉRIFIE:
1. NO-INVENTION: Chaque information ajoutée dans "Après" est-elle présente dans les sources OU dans "Avant" ?
2. SOURCES: Les sources couvrent-elles tous les nouveaux faits ?
3. RISQUE: Y a-t-il des claims sensibles (santé, certifications, garanties) ?

Retourne un JSON:
{
  "valid": true/false,
  "risk_level": "low"/"medium"/"high",
  "issues": [{"type": "unsourced_fact/invention_detected/rule_violation", "detail": "..."}],
  "requires_human_review": true/false
}

Si tout est sourcé et pas de claim sensible → valid=true, risk=low
Si sources manquantes → valid=false, issues listées
Si claims sensibles même sourcés → risk=high, requires_human_review=true

Retourne UNIQUEMENT le JSON.`, params.Field, params.Before, params.After, string(sourcesJSON))

	resp, err := t.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: t.config.OpenAI.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai validate: %w", err)
	}

	var result ValidateProposalOutput
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return json.RawMessage(resp.Choices[0].Message.Content), nil
	}

	return result, nil
}

// CommitChangesTool applies validated changes to the product
type CommitChangesTool struct{}

func (t *CommitChangesTool) Name() string { return "commit_changes" }

func (t *CommitChangesTool) Description() string {
	return "Apply validated changes to the product. Only call after validate_proposal returns valid=true."
}

func (t *CommitChangesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"changes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"field":      map[string]any{"type": "string"},
						"before":     map[string]any{"type": "string"},
						"after":      map[string]any{"type": "string"},
						"confidence": map[string]any{"type": "number"},
						"validated":  map[string]any{"type": "boolean"},
					},
				},
			},
		},
		"required": []string{"changes"},
	}
}

type CommitChange struct {
	Field      string  `json:"field"`
	Before     string  `json:"before"`
	After      string  `json:"after"`
	Confidence float64 `json:"confidence"`
	Validated  bool    `json:"validated"`
}

type CommitChangesInput struct {
	Changes []CommitChange `json:"changes"`
}

type CommitChangesOutput struct {
	Success            bool `json:"success"`
	ChangesApplied     int  `json:"changes_applied"`
	PendingHumanReview int  `json:"pending_human_review"`
}

func (t *CommitChangesTool) Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error) {
	var params CommitChangesInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	applied := 0
	pending := 0

	for _, change := range params.Changes {
		if change.Validated {
			// In a real implementation, this would update the database
			session.AddProposal(
				change.Field,
				change.Before,
				change.After,
				nil, // sources already recorded
				change.Confidence,
				"low", // already validated
			)
			applied++
		} else {
			pending++
		}
	}

	return CommitChangesOutput{
		Success:            true,
		ChangesApplied:     applied,
		PendingHumanReview: pending,
	}, nil
}

// RequestHumanReviewTool escalates to human review
type RequestHumanReviewTool struct{}

func (t *RequestHumanReviewTool) Name() string { return "request_human_review" }

func (t *RequestHumanReviewTool) Description() string {
	return "Escalate a decision to human review when uncertain or when dealing with high-risk changes"
}

func (t *RequestHumanReviewTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{
				"type":        "string",
				"description": "The question or decision requiring human input",
			},
			"context": map[string]any{
				"type":        "object",
				"description": "Relevant context for the human reviewer",
			},
			"options": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Possible options for the human to choose from",
			},
		},
		"required": []string{"question"},
	}
}

type RequestHumanReviewInput struct {
	Question string         `json:"question"`
	Context  map[string]any `json:"context,omitempty"`
	Options  []string       `json:"options,omitempty"`
}

type RequestHumanReviewOutput struct {
	ReviewID string `json:"review_id"`
	Status   string `json:"status"`
}

func (t *RequestHumanReviewTool) Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error) {
	var params RequestHumanReviewInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	// In a real implementation, this would create a review request in the database
	return RequestHumanReviewOutput{
		ReviewID: "review-pending",
		Status:   "pending",
	}, nil
}
