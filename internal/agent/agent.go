package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/benjamincozon/feedenrich/internal/agent/tools"
	"github.com/benjamincozon/feedenrich/internal/config"
	"github.com/benjamincozon/feedenrich/internal/models"
	"github.com/google/uuid"
	openai "github.com/sashabaranov/go-openai"
)

// Agent is the main enrichment agent that reasons and uses tools
type Agent struct {
	config    *config.Config
	client    *openai.Client
	toolbox   *tools.Toolbox
	callbacks Callbacks
}

// Callbacks for streaming agent events
type Callbacks struct {
	OnThought    func(thought string)
	OnToolCall   func(toolName string, input json.RawMessage)
	OnToolResult func(toolName string, output json.RawMessage)
	OnProposal   func(proposal models.Proposal)
	OnComplete   func(summary SessionSummary)
	OnError      func(err error)
}

// Session represents an active agent session
type Session struct {
	ID        uuid.UUID
	ProductID uuid.UUID
	Goal      string
	Product   *models.Product
	Traces    []models.AgentTrace
	Proposals []models.Proposal
	Sources   []models.Source
	Status    string
	StartedAt time.Time
}

// SessionSummary is returned when the agent completes
type SessionSummary struct {
	TotalSteps       int     `json:"total_steps"`
	TokensUsed       int     `json:"tokens_used"`
	DurationMs       int64   `json:"duration_ms"`
	ProposalsCreated int     `json:"proposals_created"`
	ScoreBefore      float64 `json:"score_before"`
	ScoreAfter       float64 `json:"score_after"`
}

// New creates a new Agent
func New(cfg *config.Config, toolbox *tools.Toolbox) *Agent {
	client := openai.NewClient(cfg.OpenAI.APIKey)
	return &Agent{
		config:  cfg,
		client:  client,
		toolbox: toolbox,
	}
}

// SetCallbacks sets the event callbacks
func (a *Agent) SetCallbacks(cb Callbacks) {
	a.callbacks = cb
}

// Run starts the agent on a product
func (a *Agent) Run(ctx context.Context, product *models.Product, goal string) (*Session, error) {
	session := &Session{
		ID:        uuid.New(),
		ProductID: product.ID,
		Goal:      goal,
		Product:   product,
		Traces:    []models.AgentTrace{},
		Proposals: []models.Proposal{},
		Sources:   []models.Source{},
		Status:    "running",
		StartedAt: time.Now(),
	}

	// Build initial context
	var productData models.ProductData
	if err := json.Unmarshal(product.RawData, &productData); err != nil {
		return nil, fmt.Errorf("parse product data: %w", err)
	}

	// Agent loop
	totalTokens := 0
	for step := 1; step <= a.config.Agent.MaxSteps; step++ {
		select {
		case <-ctx.Done():
			session.Status = "cancelled"
			return session, ctx.Err()
		default:
		}

		// Execute one reasoning step
		trace, tokens, done, err := a.executeStep(ctx, session, step)
		if err != nil {
			if a.callbacks.OnError != nil {
				a.callbacks.OnError(err)
			}
			session.Status = "failed"
			return session, err
		}

		totalTokens += tokens
		session.Traces = append(session.Traces, *trace)

		if done {
			break
		}
	}

	session.Status = "completed"

	// Calculate summary
	if a.callbacks.OnComplete != nil {
		summary := SessionSummary{
			TotalSteps:       len(session.Traces),
			TokensUsed:       totalTokens,
			DurationMs:       time.Since(session.StartedAt).Milliseconds(),
			ProposalsCreated: len(session.Proposals),
		}
		a.callbacks.OnComplete(summary)
	}

	return session, nil
}

func (a *Agent) executeStep(ctx context.Context, session *Session, stepNum int) (*models.AgentTrace, int, bool, error) {
	startTime := time.Now()

	// Build messages for this step
	messages := a.buildMessages(session)

	// Call OpenAI with tools
	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    a.config.OpenAI.Model,
		Messages: messages,
		Tools:    a.toolbox.OpenAITools(),
	})
	if err != nil {
		return nil, 0, false, fmt.Errorf("openai call: %w", err)
	}

	tokens := resp.Usage.TotalTokens
	choice := resp.Choices[0]

	// Check if agent wants to finish
	if choice.FinishReason == openai.FinishReasonStop {
		trace := &models.AgentTrace{
			ID:         uuid.New(),
			SessionID:  session.ID,
			StepNumber: stepNum,
			Thought:    choice.Message.Content,
			DurationMs: int(time.Since(startTime).Milliseconds()),
			TokensUsed: tokens,
			CreatedAt:  time.Now(),
		}
		return trace, tokens, true, nil
	}

	// Process tool calls
	if len(choice.Message.ToolCalls) == 0 {
		// No tool call, agent is thinking/done
		trace := &models.AgentTrace{
			ID:         uuid.New(),
			SessionID:  session.ID,
			StepNumber: stepNum,
			Thought:    choice.Message.Content,
			DurationMs: int(time.Since(startTime).Milliseconds()),
			TokensUsed: tokens,
			CreatedAt:  time.Now(),
		}
		if a.callbacks.OnThought != nil && choice.Message.Content != "" {
			a.callbacks.OnThought(choice.Message.Content)
		}
		return trace, tokens, true, nil
	}

	// Execute the first tool call
	toolCall := choice.Message.ToolCalls[0]
	toolName := toolCall.Function.Name
	toolInput := json.RawMessage(toolCall.Function.Arguments)

	if a.callbacks.OnToolCall != nil {
		a.callbacks.OnToolCall(toolName, toolInput)
	}

	// Execute the tool
	toolOutput, err := a.toolbox.Execute(ctx, toolName, toolInput, session)
	if err != nil {
		return nil, tokens, false, fmt.Errorf("tool %s: %w", toolName, err)
	}

	toolOutputJSON, _ := json.Marshal(toolOutput)

	if a.callbacks.OnToolResult != nil {
		a.callbacks.OnToolResult(toolName, toolOutputJSON)
	}

	trace := &models.AgentTrace{
		ID:         uuid.New(),
		SessionID:  session.ID,
		StepNumber: stepNum,
		Thought:    choice.Message.Content,
		ToolName:   toolName,
		ToolInput:  toolInput,
		ToolOutput: toolOutputJSON,
		DurationMs: int(time.Since(startTime).Milliseconds()),
		TokensUsed: tokens,
		CreatedAt:  time.Now(),
	}

	// Check for special completion tools
	if toolName == "commit_changes" || toolName == "finish" {
		return trace, tokens, true, nil
	}

	return trace, tokens, false, nil
}

func (a *Agent) buildMessages(session *Session) []openai.ChatCompletionMessage {
	// System prompt
	systemPrompt := fmt.Sprintf(`Tu es un agent d'enrichissement de données produit. 

OBJECTIF: %s

CONTRAINTES ABSOLUES:
1. Tu ne dois JAMAIS inventer une caractéristique produit non sourcée
2. Chaque fait ajouté DOIT avoir une source (feed, web, ou vision)
3. Si tu n'es pas sûr → utilise request_human_review
4. Toujours validate_proposal avant commit_changes
5. Pour les attributs techniques (matière, dimensions, poids) → source web obligatoire
6. Pour les attributs visuels (couleur, style) → vision acceptable si confidence > 0.85

PROCESSUS:
1. D'abord, utilise analyze_product pour comprendre l'état actuel
2. Identifie les problèmes et opportunités
3. Cherche des informations avec web_search et fetch_page
4. Utilise analyze_image pour confirmer visuellement
5. Optimise les champs avec optimize_field
6. Ajoute les attributs manquants avec add_attribute
7. Valide avec validate_proposal
8. Commit avec commit_changes

Sois méthodique et cite toujours tes sources.`, session.Goal)

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
	}

	// Add product context
	productJSON, _ := json.MarshalIndent(session.Product.RawData, "", "  ")
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: fmt.Sprintf("Voici le produit à enrichir:\n\n```json\n%s\n```", string(productJSON)),
	})

	// Add previous steps as context
	for _, trace := range session.Traces {
		if trace.Thought != "" {
			messages = append(messages, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: trace.Thought,
			})
		}
		if trace.ToolName != "" {
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleAssistant,
				Content:    "",
				ToolCalls: []openai.ToolCall{{
					ID:   fmt.Sprintf("call_%d", trace.StepNumber),
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      trace.ToolName,
						Arguments: string(trace.ToolInput),
					},
				}},
			})
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    string(trace.ToolOutput),
				ToolCallID: fmt.Sprintf("call_%d", trace.StepNumber),
			})
		}
	}

	return messages
}
