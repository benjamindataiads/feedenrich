package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// CopyExecutionAgent is the WRITER agent.
// It works UNDER CONSTRAINTS set by the Planner.
// It receives: allowed facts, forbidden facts, hard rules, objective
// It is NOT allowed to invent.
type CopyExecutionAgent struct {
	client *openai.Client
	config *config.Config
}

func NewCopyExecutionAgent(cfg *config.Config) *CopyExecutionAgent {
	return &CopyExecutionAgent{
		client: openai.NewClient(cfg.OpenAI.APIKey),
		config: cfg,
	}
}

// WriterInput contains the constrained writing task
type WriterInput struct {
	Field          string            `json:"field"`
	CurrentValue   string            `json:"current_value"`
	Objective      string            `json:"objective"`
	AllowedFacts   map[string]string `json:"allowed_facts"`   // field -> value (verified)
	ForbiddenFacts []string          `json:"forbidden_facts"` // cannot use these
	Constraints    []string          `json:"constraints"`     // rules to follow
}

// WriterOutput contains the generated copy with justification
type WriterOutput struct {
	Before        string      `json:"before"`
	After         string      `json:"after"`
	Justification string      `json:"justification"`
	FactsUsed     []FactUsage `json:"facts_used"`
	Confidence    float64     `json:"confidence"`
}

type FactUsage struct {
	Fact   string `json:"fact"`
	Source string `json:"source"` // which allowed_fact key was used
}

// Execute generates optimized copy under strict constraints
func (w *CopyExecutionAgent) Execute(ctx context.Context, input WriterInput) (*WriterOutput, error) {
	allowedJSON, _ := json.MarshalIndent(input.AllowedFacts, "", "  ")
	forbiddenJSON, _ := json.Marshal(input.ForbiddenFacts)
	constraintsJSON, _ := json.Marshal(input.Constraints)

	prompt := fmt.Sprintf(`You are a COPY EXECUTION AGENT. You write UNDER STRICT CONSTRAINTS.

CRITICAL CONSTRAINTS:
- You can ONLY use facts from the ALLOWED_FACTS list
- You CANNOT use or infer anything from FORBIDDEN_FACTS
- You MUST follow all CONSTRAINTS
- You are NOT allowed to invent information
- Every fact you use must be traceable to ALLOWED_FACTS

TASK:
- Field: %s
- Current value: %s
- Objective: %s

ALLOWED FACTS (you can ONLY use these):
%s

FORBIDDEN (you CANNOT use or infer):
%s

CONSTRAINTS TO FOLLOW:
%s

OUTPUT FORMAT (JSON only):
{
  "before": "original value",
  "after": "optimized value",
  "justification": "brief explanation of changes made",
  "facts_used": [
    { "fact": "what was used", "source": "which allowed_fact key" }
  ],
  "confidence": 0.0-1.0
}

CONFIDENCE GUIDELINES:
- 1.0: All facts from verified sources, no inference
- 0.8-0.9: Minor rewording of verified facts
- 0.6-0.8: Some restructuring required
- <0.6: Significant changes, higher uncertainty

Return ONLY the JSON, no explanations.`, input.Field, input.CurrentValue, input.Objective, string(allowedJSON), string(forbiddenJSON), string(constraintsJSON))

	resp, err := w.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: w.config.OpenAI.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("writer call failed: %w", err)
	}

	var output WriterOutput
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &output); err != nil {
		return nil, fmt.Errorf("parse writer output: %w", err)
	}

	// Ensure before value matches input
	output.Before = input.CurrentValue

	return &output, nil
}
