package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	OnLog        func(message string) // General logging for UI visibility
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
	var imageContext string
	var imageAnalysisStatus string
	
	// SMART OPTIMIZATION: Check if feed already has visual attributes
	// If feed is "complete enough", skip expensive image analysis
	missingVisualAttrs := checkMissingVisualAttributes(product.RawData)
	needsImageAnalysis := len(missingVisualAttrs) >= 2 // Only if 2+ visual attrs missing
	
	imageURL := extractImageURL(product.RawData)
	
	if imageURL == "" {
		imageAnalysisStatus = "no_image_url"
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog("⚠️ No image URL - using feed data only")
		}
	} else if !needsImageAnalysis {
		// SKIP image analysis - feed is complete enough
		imageAnalysisStatus = "skipped_feed_complete"
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("⚡ Feed has visual attrs - skipping image analysis (missing only: %v)", missingVisualAttrs))
		}
	} else {
		// Need image analysis - visual attributes are missing
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("👁️ Missing %d visual attrs %v - analyzing image...", len(missingVisualAttrs), missingVisualAttrs))
		}
		
		// Quick image analysis - only extract what's missing
		imgResp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model: openai.GPT4oMini,
			Messages: []openai.ChatCompletionMessage{
				{
					Role: openai.ChatMessageRoleUser,
					MultiContent: []openai.ChatMessagePart{
						{
							Type: openai.ChatMessagePartTypeText,
							Text: fmt.Sprintf(`Extract these MISSING attributes from the image: %v
Output JSON with ONLY these fields. Be concise. Use null if not visible.`, missingVisualAttrs),
						},
						{
							Type:     openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{URL: imageURL},
						},
					},
				},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
			MaxTokens:      150, // Reduced - only extracting specific fields
			Temperature:    0.1,
		})
		
		if err != nil {
			imageAnalysisStatus = "error"
			if a.callbacks.OnLog != nil {
				a.callbacks.OnLog(fmt.Sprintf("❌ Image analysis failed: %v", err))
			}
		} else if len(imgResp.Choices) > 0 {
			imageContext = "\n\n=== IMAGE ANALYSIS (for missing attrs) ===\n" + imgResp.Choices[0].Message.Content
			imageAnalysisStatus = "success"
			a.recordUsage(ctx, openai.GPT4oMini, imgResp.Usage)
			
			if a.callbacks.OnLog != nil {
				a.callbacks.OnLog(fmt.Sprintf("✅ Image: %s", imgResp.Choices[0].Message.Content))
			}
		}
	}
	
	// Log source aggregation
	if a.callbacks.OnLog != nil {
		if imageAnalysisStatus == "skipped_feed_complete" {
			a.callbacks.OnLog("⚡ FAST MODE: Feed data sufficient")
		} else if imageAnalysisStatus == "success" {
			a.callbacks.OnLog("🔄 Combining: Feed + Image")
		} else {
			a.callbacks.OnLog("📄 Using: Feed data only")
		}
	}

	// Main optimization call
	systemPrompt := `You are a GMC (Google Merchant Center) product data optimizer. Analyze and generate optimization proposals.

=== MULTILINGUAL FIELD NAMES ===
Product data may contain fields in French or other languages. Common mappings:
- titre/nom/libellé → title
- lien/url → link  
- lien_image/image → image_link
- prix → price
- couleur/coloris → color
- taille/pointure → size
- genre/sexe → gender
- âge/tranche_d_age → age_group
- matière/tissu → material
- état → condition
- marque → brand
- catégorie → product_type

Always interpret these as their GMC English equivalents when analyzing.

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

3. MISSING ATTRIBUTES - ALWAYS PROPOSE IF EMPTY:
   
   REQUIRED FOR APPAREL (MUST fill if empty):
   - color: From image analysis or title/description
   - gender: From product type, title, image (male/female/unisex)
   - age_group: Default "adult" unless kids/baby product → ALWAYS PROPOSE IF EMPTY
   - size: Extract from title/description if present
   
   STRONGLY RECOMMENDED (MUST fill if empty):
   - condition: Default "new" unless indicated otherwise → ALWAYS PROPOSE IF EMPTY
   - product_type: Build hierarchy from category/title (e.g., "Apparel > Women > Dresses")
   - google_product_category: Map to Google taxonomy ID
   
   SIZE DETAILS (IMPORTANT for apparel):
   - size_system: Infer from MULTIPLE signals (priority order):
     1. SIZE VALUE PATTERNS:
        * Numeric 34-50 (e.g., "38", "42", "44/46") → "EU"
        * Letters S/M/L/XL/XXL → "US" or "UK" (check link domain)
        * UK sizes 6-20 (women) or 34-48 (men) → "UK"
        * US sizes 0-16 (women) or 28-44 (men) → "US"
        * Shoe sizes 35-48 → "EU", 5-15 → US/UK
     2. LINK/URL DOMAIN:
        * .fr, .de, .it, .es, .eu → "EU"
        * .co.uk → "UK"
        * .com (with USD) → "US"
     3. CURRENCY in price field:
        * EUR/€ → "EU"
        * GBP/£ → "UK"
        * USD/$ → "US"
     4. Default → "EU" for European merchants
     → ALWAYS PROPOSE IF EMPTY FOR APPAREL
   - size_type: "regular" by default, "plus"/"petite"/"tall"/"maternity" if indicated
   
   VISUAL ATTRIBUTES (from image):
   - material: From image or product type (cotton, polyester, leather...)
   - pattern: From image (solid, striped, floral, checkered, printed...)

4. CONFIDENCE LEVELS:
   - HIGH (0.9+): Direct from feed data or clear image observation
   - MEDIUM (0.7-0.9): Reasonably inferred from context
   - LOW (0.5-0.7): Educated guess, needs human review

5. RISK LEVELS:
   - low: Text improvements, color from image, gender from obvious cues
   - medium: Material inference, category mapping
   - high: Technical specs, safety claims, compatibility → flag for human review

=== SOURCE RECONCILIATION RULES ===
When multiple sources provide data for the same field, use this priority:

1. FEED DATA (highest priority for identifiers):
   - ALWAYS trust feed for: id, gtin, mpn, brand, price, link, sku
   - These are business-critical and should never be overwritten

2. IMAGE ANALYSIS (highest priority for visual attributes):
   - Trust image for: color, pattern, material, style, product_type
   - If feed has color="N/A" or empty but image shows "blue" → use "blue"
   - If feed has color="rouge" and image shows "red" → use "red" (standardize)

3. FEED + IMAGE (combined for text content):
   - TITLE: Start with feed title, ENRICH with image attributes (color, material, style)
     Example: "T-shirt" → "T-shirt bleu en coton à rayures" (if image shows blue cotton stripes)
   - DESCRIPTION: Start with feed description, ADD image observations:
     * Add color details seen in image
     * Add material if visible
     * Add style/design elements
     * Add any visible features (logos, buttons, patterns)
     Example: "Robe élégante" → "Robe élégante rouge en soie avec motif floral. Col V et manches longues."

4. INFERENCE (lowest priority):
   - Use for: age_group (default "adult"), condition (default "new"), size_system
   - Only when no explicit data from feed or image

=== CONFLICT RESOLUTION ===
- Feed says "color: bleu", Image says "color: navy blue" → Use "navy blue" (more specific)
- Feed says "material: fabric", Image says "material: cotton" → Use "cotton" (more specific)
- Feed says "gender: unisex", Image clearly shows women's dress → Use "female"
- Feed has value, Image has null → Keep feed value
- Feed empty, Image has value → Use image value

=== TITLE & DESCRIPTION ENRICHMENT ===
ALWAYS combine feed + image for title and description:
- Title template: [Brand] + [Product Type] + [Color] + [Material] + [Key Feature]
  Feed: "Robe Zara" + Image: {color:"rouge", material:"soie", pattern:"floral"}
  → "Robe Zara rouge en soie motif floral"
  
- Description: Keep feed text + append image observations
  Feed: "Belle robe pour soirée"
  Image: {color:"rouge", material:"soie", style:"élégant", observations:["col V","manches longues"]}
  → "Belle robe pour soirée. Couleur rouge vif en soie légère. Coupe élégante avec col V et manches longues."

=== CRITICAL RULES ===
- NO INVENTION: Only use facts from feed data or image analysis
- Be GENEROUS: Propose improvements that could be rejected rather than miss opportunities
- Generate AT LEAST 3-5 proposals for any product with room for improvement
- ALWAYS fill these if empty: condition (→"new"), age_group (→"adult"), size_system (→infer from currency)
- For APPAREL: ALWAYS check AND PROPOSE: color, gender, age_group, size, size_system, condition
- DO NOT skip fields just because they seem "optional" - GMC rewards completeness
- ALWAYS specify the source in your proposal: "feed", "image", or "inferred"`

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
	
	// Check all possible image field names (English + French + variants)
	imageFields := []string{
		// GMC standard
		"image_link", "image link", "imageLink",
		// Common variants
		"image", "Image", "image_url", "imageUrl", "ImageURL",
		"main_image", "mainImage", "primary_image",
		"picture", "Picture", "photo", "Photo",
		// French
		"lien_image", "lien image", "url_image", "url image",
		"image_produit", "photo_produit",
		// Additional image links (use first if main missing)
		"additional_image_link", "additional_image_links",
	}
	
	for _, key := range imageFields {
		if val, ok := fields[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				// Validate it looks like a URL
				if strings.HasPrefix(str, "http://") || strings.HasPrefix(str, "https://") {
					return str
				}
			}
		}
	}
	
	// Also check case-insensitive
	for k, v := range fields {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "image") || strings.Contains(lower, "photo") || strings.Contains(lower, "picture") {
			if str, ok := v.(string); ok && str != "" {
				if strings.HasPrefix(str, "http://") || strings.HasPrefix(str, "https://") {
					return str
				}
			}
		}
	}
	
	return ""
}

func truncateURL(url string, maxLen int) string {
	if len(url) <= maxLen {
		return url
	}
	return url[:maxLen-3] + "..."
}

// checkMissingVisualAttributes returns list of visual attributes that are empty/missing
func checkMissingVisualAttributes(data json.RawMessage) []string {
	var fields map[string]interface{}
	json.Unmarshal(data, &fields)
	
	// Visual attributes that can be extracted from images
	visualAttrs := []string{"color", "couleur", "material", "matière", "matiere", "pattern", "motif", "gender", "genre", "age_group", "product_type"}
	
	var missing []string
	for _, attr := range visualAttrs {
		val := getFieldValueFromMap(fields, attr)
		if val == "" || val == "N/A" || val == "n/a" || val == "-" {
			// Normalize to English
			switch attr {
			case "couleur":
				attr = "color"
			case "matière", "matiere":
				attr = "material"
			case "motif":
				attr = "pattern"
			case "genre":
				attr = "gender"
			}
			// Avoid duplicates
			found := false
			for _, m := range missing {
				if m == attr {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, attr)
			}
		}
	}
	return missing
}

func getFieldValueFromMap(fields map[string]interface{}, key string) string {
	// Try exact match
	if val, ok := fields[key]; ok {
		if str, ok := val.(string); ok {
			return strings.TrimSpace(str)
		}
	}
	// Try lowercase
	for k, v := range fields {
		if strings.ToLower(k) == strings.ToLower(key) {
			if str, ok := v.(string); ok {
				return strings.TrimSpace(str)
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
