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
	// System prompt based on Dataïads GMC Feed Optimization Methodology
	systemPrompt := fmt.Sprintf(`Tu es un agent d'enrichissement de données produit pour Google Merchant Center.

OBJECTIF: %s

=== MÉTHODOLOGIE D'OPTIMISATION FEED (Dataïads) ===

FLUX DE PRIORITÉ:
1. 🔴 ERREURS CRITIQUES (100%% SAFE - Fix immédiat)
   - Policy violations, price mismatch, availability mismatch
   - Invalid URLs, Invalid GTIN, Image policy
   
2. 🟠 ATTRIBUTS OBLIGATOIRES (100%% SAFE)
   - id, title, description, brand, gtin/mpn, condition
   
3. 🟡 ATTRIBUTS RECOMMANDÉS (100%% SAFE)
   - google_product_category, product_type, color/size/material
   - item_group_id, gender/age_group, shipping
   
4. 🟢 OPTIMISATION TITRES (A/B TEST requis)
   Templates par catégorie:
   - Apparel: {brand} + {gender} + {type} + {color} + {size} + {material}
   - Electronics: {brand} + {line} + {model} + {key_spec} + {capacity}
   - Home & Garden: {brand} + {type} + {material} + {dimensions} + {style}
   - Beauty: {brand} + {line} + {type} + {variant} + {size}
   
   Best practices:
   ✅ Front-load keywords (70 premiers chars visibles)
   ✅ Max 150 chars, optimal 70-100 chars
   ❌ PAS de MAJUSCULES abusives
   ❌ PAS de texte promo (SOLDES, -50%%, etc.)
   ❌ PAS de symboles ★ ♥ →

5. 🔵 OPTIMISATION DESCRIPTIONS
   Structure: Accroche → Features → Specs → Use cases
   ✅ Min 500 chars, contenu unique
   ❌ PAS de HTML, prix, liens externes

=== CONTRAINTES "NO INVENTION" ===
1. Tu ne dois JAMAIS inventer une caractéristique produit non sourcée
2. Chaque fait ajouté DOIT avoir une source:
   - "feed": données existantes du fichier
   - "web": source vérifiée (URL citée)
   - "vision": observation image (confidence > 0.85)
3. Si incertain → request_human_review
4. Toujours validate_proposal avant commit

=== NIVEAUX DE RISQUE ===
- LOW: Corrections format, case, attributs du feed, couleur image évidente
- MEDIUM: Restructuration titre, réécriture description, web sources
- HIGH: Specs techniques, claims compatibilité, santé/sécurité → HUMAN REVIEW

=== PROCESSUS ===
1. analyze_product → évaluer qualité et conformité GMC
2. web_search/fetch_page → sourcer informations manquantes
3. analyze_image → confirmer visuellement (couleur, style, matériau)
4. optimize_field → titres/descriptions avec templates
5. add_attribute → ajouter attributs avec sources
6. validate_proposal → vérifier no-invention
7. commit_changes → finaliser

Sois méthodique, cite toujours tes sources, respecte la hiérarchie des priorités.`, session.Goal)

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
