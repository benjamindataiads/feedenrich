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

// TokenTracker interface for recording token usage
type TokenTracker interface {
	RecordTokenUsage(ctx context.Context, model string, promptTokens, completionTokens int, costUSD float64) error
}

// Agent is the main enrichment agent that reasons and uses tools
type Agent struct {
	config       *config.Config
	client       *openai.Client
	toolbox      *tools.Toolbox
	callbacks    Callbacks
	tokenTracker TokenTracker
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

// SetTokenTracker sets the token tracker for recording usage
func (a *Agent) SetTokenTracker(tracker TokenTracker) {
	a.tokenTracker = tracker
}

// recordUsage records token usage to the database
func (a *Agent) recordUsage(ctx context.Context, model string, usage openai.Usage) {
	if a.tokenTracker == nil {
		return
	}
	
	// Calculate cost based on model
	// GPT-4o-mini pricing (as of 2024): $0.15/1M input, $0.60/1M output
	// GPT-4o pricing: $2.50/1M input, $10.00/1M output
	var costUSD float64
	switch model {
	case openai.GPT4oMini, openai.GPT4oMini20240718:
		costUSD = float64(usage.PromptTokens)*0.00000015 + float64(usage.CompletionTokens)*0.0000006
	case openai.GPT4o, openai.GPT4o20240513:
		costUSD = float64(usage.PromptTokens)*0.0000025 + float64(usage.CompletionTokens)*0.00001
	default:
		// Default to GPT-4o-mini pricing
		costUSD = float64(usage.PromptTokens)*0.00000015 + float64(usage.CompletionTokens)*0.0000006
	}
	
	_ = a.tokenTracker.RecordTokenUsage(ctx, model, usage.PromptTokens, usage.CompletionTokens, costUSD)
}

// Run starts the agent on a product - uses FAST mode by default (single API call)
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

	// Use FAST mode: single consolidated API call
	proposals, err := a.runFastMode(ctx, product)
	if err != nil {
		if a.callbacks.OnError != nil {
			a.callbacks.OnError(err)
		}
		session.Status = "failed"
		return session, err
	}

	session.Proposals = proposals
	session.Status = "completed"

	// Single trace for the fast execution
	session.Traces = append(session.Traces, models.AgentTrace{
		ID:         uuid.New(),
		SessionID:  session.ID,
		StepNumber: 1,
		Thought:    fmt.Sprintf("Fast mode: analyzed product and generated %d proposals", len(proposals)),
		ToolName:   "fast_optimize",
		DurationMs: int(time.Since(session.StartedAt).Milliseconds()),
		CreatedAt:  time.Now(),
	})

	if a.callbacks.OnComplete != nil {
		summary := SessionSummary{
			TotalSteps:       1,
			TokensUsed:       0, // Not tracked in fast mode
			DurationMs:       time.Since(session.StartedAt).Milliseconds(),
			ProposalsCreated: len(session.Proposals),
		}
		a.callbacks.OnComplete(summary)
	}

	return session, nil
}

// runFastMode executes optimization in a single API call
func (a *Agent) runFastMode(ctx context.Context, product *models.Product) ([]models.Proposal, error) {
	// Extract image URL for parallel analysis
	var imageContext string
	imageURL := extractImageURL(product.RawData)
	if imageURL != "" {
		// Quick image analysis
		imgResp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model: openai.GPT4oMini,
			Messages: []openai.ChatCompletionMessage{
				{
					Role: openai.ChatMessageRoleUser,
					MultiContent: []openai.ChatMessagePart{
						{
							Type: openai.ChatMessagePartTypeText,
							Text: `Extract ALL GMC attributes from this image. Output JSON: {"color":"primary color","secondary_colors":[],"material":"if visible","pattern":"solid/striped/floral/etc","style":"casual/formal/sporty","gender":"male/female/unisex if obvious","age_group":"adult/kids if obvious","product_type":"what the product is","observations":["other facts"]}. Only state what you clearly see.`,
						},
						{
							Type:     openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{URL: imageURL},
						},
					},
				},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
			MaxTokens:      200,
			Temperature:    0.1,
		})
		if err == nil && len(imgResp.Choices) > 0 {
			imageContext = "\n\nImage Analysis: " + imgResp.Choices[0].Message.Content
			// Track image analysis tokens
			a.recordUsage(ctx, openai.GPT4oMini, imgResp.Usage)
		}
	}

	// Main optimization call
	systemPrompt := `You are a GMC (Google Merchant Center) product data optimizer. Analyze and generate optimization proposals.

=== GMC ATTRIBUTES REFERENCE (2025) ===

REQUIRED ATTRIBUTES:
- id: Unique product identifier
- title: Product name (30-150 chars, include brand + type + key attributes)
- description: Product description (50-5000 chars, informative)
- link: Product page URL
- image_link: Main product image URL
- price: Product price with currency
- availability: in_stock, out_of_stock, preorder, backorder
- brand: Manufacturer/brand name (required for most categories)

REQUIRED FOR APPAREL (US, UK, DE, JP, FR, BR):
- color: Product color (required for apparel) - use standard names, no hex codes
- gender: male, female, unisex (required for apparel)
- age_group: newborn, infant, toddler, kids, adult (required for apparel)
- size: Product size (required for clothing/shoes) - S, M, L, XL or numeric

STRONGLY RECOMMENDED:
- gtin: EAN/UPC/ISBN (13 digits for EAN, 12 for UPC)
- mpn: Manufacturer Part Number (if no GTIN)
- google_product_category: Google taxonomy ID
- product_type: Your category hierarchy (e.g., "Apparel > Shirts > T-Shirts")
- condition: new, used, refurbished (default: new)
- item_group_id: Required for variants (same product, different size/color)

OPTIONAL BUT VALUABLE:
- material: Fabric/material (e.g., "cotton", "leather", "polyester")
- pattern: Pattern name (e.g., "striped", "floral", "solid")
- size_type: regular, petite, plus, tall, big, maternity
- size_system: US, UK, EU, DE, FR, IT, AU, BR, CN, JP
- additional_image_link: Up to 10 extra images
- sale_price: Discounted price with sale dates
- shipping_weight: For shipping calculations
- product_weight: Actual product weight
- product_length, product_width, product_height: Dimensions

=== OUTPUT FORMAT (JSON) ===
{
  "score": 0.65,
  "missing_attributes": ["color", "gender", "age_group"],
  "proposals": [
    {
      "field": "title",
      "before": "current value",
      "after": "optimized value", 
      "rationale": "why this change improves the product",
      "source": "feed|image|inferred",
      "confidence": 0.9,
      "risk_level": "low|medium|high"
    }
  ]
}

=== OPTIMIZATION RULES ===

1. TITLE OPTIMIZATION (ALWAYS check):
   - Min 30 chars, optimal 60-150 chars
   - Template: Brand + Gender + Product Type + Key Attributes (color, size, material)
   - Front-load important keywords (first 70 chars visible in search)
   - NO promotional text (free shipping, sale, discount, -50%)
   
2. DESCRIPTION OPTIMIZATION (ALWAYS check):
   - Min 50 chars, optimal 100-500 chars
   - Include: benefits, features, use cases, specifications
   - NO promotional text or ALL CAPS

3. MISSING ATTRIBUTES (ALWAYS propose if inferable):
   - color: Infer from image or title/description
   - gender: Infer from product type, title, or image
   - age_group: Default to "adult" for non-kids products
   - material: Infer from image or product type
   - pattern: Infer from image (striped, solid, floral, etc.)
   - size: Extract from title if present
   - product_type: Build hierarchy from category/title
   - google_product_category: Map to Google taxonomy

4. CONFIDENCE LEVELS:
   - HIGH (0.9+): Direct from feed data or clear image observation
   - MEDIUM (0.7-0.9): Reasonably inferred from context
   - LOW (0.5-0.7): Educated guess, needs human review

5. RISK LEVELS:
   - low: Text improvements, color from image, gender from obvious cues
   - medium: Material inference, category mapping
   - high: Technical specs, safety claims, compatibility → flag for human review

=== CRITICAL RULES ===
- NO INVENTION: Only use facts from feed data or image analysis
- Be GENEROUS: Propose improvements that could be rejected rather than miss opportunities
- Generate AT LEAST 2-3 proposals for any product with room for improvement
- For APPAREL: ALWAYS check color, gender, age_group, size`

	userPrompt := fmt.Sprintf("Product Data:\n%s%s\n\nGenerate optimization proposals.", string(product.RawData), imageContext)

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT4oMini,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
		Temperature:    0.3,
	})
	if err != nil {
		return nil, fmt.Errorf("optimization call failed: %w", err)
	}

	// Track main optimization tokens
	a.recordUsage(ctx, openai.GPT4oMini, resp.Usage)

	// Parse response
	var output struct {
		Score     float64 `json:"score"`
		Proposals []struct {
			Field      string  `json:"field"`
			Before     string  `json:"before"`
			After      string  `json:"after"`
			Rationale  string  `json:"rationale"`
			Source     string  `json:"source"`
			Confidence float64 `json:"confidence"`
			RiskLevel  string  `json:"risk_level"`
		} `json:"proposals"`
	}

	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &output); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// Convert to models.Proposal
	var proposals []models.Proposal
	for _, p := range output.Proposals {
		// Skip invalid proposals
		if p.After == "" || p.After == p.Before {
			continue
		}
		if p.Confidence < 0.3 {
			continue
		}

		beforeValue := p.Before
		sourceJSON, _ := json.Marshal([]models.Source{{Type: p.Source, Confidence: p.Confidence}})

		proposal := models.Proposal{
			ID:          uuid.New(),
			ProductID:   product.ID,
			Field:       p.Field,
			BeforeValue: &beforeValue,
			AfterValue:  p.After,
			Rationale:   []string{p.Rationale},
			Sources:     sourceJSON,
			Confidence:  p.Confidence,
			RiskLevel:   p.RiskLevel,
			Status:      "proposed",
			CreatedAt:   time.Now(),
		}
		proposals = append(proposals, proposal)

		if a.callbacks.OnProposal != nil {
			a.callbacks.OnProposal(proposal)
		}
	}

	return proposals, nil
}

func extractImageURL(data json.RawMessage) string {
	var fields map[string]interface{}
	json.Unmarshal(data, &fields)
	for _, key := range []string{"image_link", "image link", "imageLink", "image", "Image"} {
		if val, ok := fields[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				return str
			}
		}
	}
	return ""
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
