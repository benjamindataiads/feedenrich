package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// OptimizationGroup represents a focused optimization category
type OptimizationGroup string

const (
	GroupCriticalErrors      OptimizationGroup = "critical_errors"
	GroupRequiredAttributes  OptimizationGroup = "required_attributes"
	GroupRecommendedAttrs    OptimizationGroup = "recommended_attributes"
	GroupTitleOptimization   OptimizationGroup = "title_optimization"
	GroupDescOptimization    OptimizationGroup = "description_optimization"
	GroupImageAnalysis       OptimizationGroup = "image_analysis"
	GroupPricingPromotions   OptimizationGroup = "pricing_promotions"
	GroupAll                 OptimizationGroup = "all" // Default - all optimizations
)

// GroupInfo provides metadata about an optimization group
type GroupInfo struct {
	ID          OptimizationGroup `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Fields      []string          `json:"fields"`
	Safe        bool              `json:"safe"` // 100% safe = immediate rollout
	Icon        string            `json:"icon"`
}

// GetAllGroups returns all available optimization groups
func GetAllGroups() []GroupInfo {
	return []GroupInfo{
		{
			ID:          GroupCriticalErrors,
			Name:        "Critical Errors",
			Description: "Fix policy violations, price/availability mismatch, invalid URLs/GTINs, image issues",
			Fields:      []string{"link", "image_link", "price", "availability", "gtin"},
			Safe:        true,
			Icon:        "🔴",
		},
		{
			ID:          GroupRequiredAttributes,
			Name:        "Required Attributes",
			Description: "Complete mandatory fields: id, title, description, brand, gtin/mpn, condition",
			Fields:      []string{"id", "title", "description", "brand", "gtin", "mpn", "condition"},
			Safe:        true,
			Icon:        "🟠",
		},
		{
			ID:          GroupRecommendedAttrs,
			Name:        "Recommended Attributes",
			Description: "Enrich with google_product_category, product_type, color, size, material, gender, age_group",
			Fields:      []string{"google_product_category", "product_type", "color", "size", "material", "gender", "age_group", "item_group_id"},
			Safe:        true,
			Icon:        "🟡",
		},
		{
			ID:          GroupTitleOptimization,
			Name:        "Title Optimization",
			Description: "Structure titles with category templates: Brand + Type + Color + Size + Material",
			Fields:      []string{"title"},
			Safe:        false, // A/B test recommended
			Icon:        "🟢",
		},
		{
			ID:          GroupDescOptimization,
			Name:        "Description Optimization",
			Description: "Enhance descriptions with structure: Accroche → Features → Specs → Use cases",
			Fields:      []string{"description", "product_highlight", "product_detail"},
			Safe:        false, // A/B test recommended
			Icon:        "🟢",
		},
		{
			ID:          GroupImageAnalysis,
			Name:        "Image Analysis",
			Description: "Analyze image quality: count, resolution, aspect ratio, background, framing",
			Fields:      []string{"image_link", "additional_image_link"},
			Safe:        true,
			Icon:        "🔵",
		},
		{
			ID:          GroupPricingPromotions,
			Name:        "Pricing & Promotions",
			Description: "Validate pricing structure, sale prices, promotion dates",
			Fields:      []string{"price", "sale_price", "sale_price_effective_date", "promotion_id"},
			Safe:        true,
			Icon:        "💙",
		},
	}
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
	return a.RunWithGroup(ctx, product, goal, GroupAll)
}

// RunWithGroup starts the agent on a product with a specific optimization group
func (a *Agent) RunWithGroup(ctx context.Context, product *models.Product, goal string, group OptimizationGroup) (*Session, error) {
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

	// Use group-specific optimization
	proposals, err := a.runGroupOptimization(ctx, product, group)
	if err != nil {
		if a.callbacks.OnError != nil {
			a.callbacks.OnError(err)
		}
		session.Status = "failed"
		return session, err
	}

	session.Proposals = proposals
	session.Status = "completed"

	// Single trace for the execution
	session.Traces = append(session.Traces, models.AgentTrace{
		ID:         uuid.New(),
		SessionID:  session.ID,
		StepNumber: 1,
		Thought:    fmt.Sprintf("Group %s: analyzed product and generated %d proposals", group, len(proposals)),
		ToolName:   string(group),
		DurationMs: int(time.Since(session.StartedAt).Milliseconds()),
		CreatedAt:  time.Now(),
	})

	if a.callbacks.OnComplete != nil {
		summary := SessionSummary{
			TotalSteps:       1,
			TokensUsed:       0,
			DurationMs:       time.Since(session.StartedAt).Milliseconds(),
			ProposalsCreated: len(session.Proposals),
		}
		a.callbacks.OnComplete(summary)
	}

	return session, nil
}

// runGroupOptimization runs optimization for a specific group
func (a *Agent) runGroupOptimization(ctx context.Context, product *models.Product, group OptimizationGroup) ([]models.Proposal, error) {
	if group == GroupAll {
		return a.runFastMode(ctx, product)
	}
	
	// For specific groups, use focused prompts
	return a.runFocusedMode(ctx, product, group)
}

// runFastMode executes optimization in a single API call
func (a *Agent) runFastMode(ctx context.Context, product *models.Product) ([]models.Proposal, error) {
	var imageContext string
	var webContext string
	
	// === 1. IMAGE ANALYSIS (ALWAYS if URL available) ===
	imageURL := extractImageURL(product.RawData)
	
	if imageURL == "" {
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog("⚠️ No image URL - skipping image analysis")
		}
	} else {
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog("👁️ Analyzing product image...")
		}
		
		// Full image analysis - extract ALL visual attributes
		imgResp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model: openai.GPT4oMini,
			Messages: []openai.ChatCompletionMessage{
				{
					Role: openai.ChatMessageRoleUser,
					MultiContent: []openai.ChatMessagePart{
						{
							Type: openai.ChatMessagePartTypeText,
							Text: `Analyze this product image. Extract ALL visible attributes:
{
  "color": "main color(s)",
  "material": "visible material (cotton, leather, metal, etc.)",
  "pattern": "pattern if any (solid, striped, floral, etc.)",
  "gender": "target gender if obvious (male/female/unisex)",
  "product_type": "what type of product",
  "style": "style description",
  "observations": ["list of additional visual details"]
}
Use null for attributes not clearly visible. Be precise and factual.`,
						},
						{
							Type:     openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{URL: imageURL},
						},
					},
				},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
			MaxTokens:      250,
			Temperature:    0.1,
		})
		
		if err != nil {
			if a.callbacks.OnLog != nil {
				a.callbacks.OnLog(fmt.Sprintf("❌ Image analysis failed: %v", err))
			}
		} else if len(imgResp.Choices) > 0 {
			imageContext = "\n\n=== IMAGE ANALYSIS ===\n" + imgResp.Choices[0].Message.Content
			a.recordUsage(ctx, openai.GPT4oMini, imgResp.Usage)
			
			if a.callbacks.OnLog != nil {
				a.callbacks.OnLog(fmt.Sprintf("✅ Image: %s", imgResp.Choices[0].Message.Content))
			}
		}
	}
	
	// === 2. WEB SEARCH (if GTIN/EAN or brand+title available) ===
	webContext = a.runWebSearch(ctx, product)
	
	// Log source aggregation
	sources := []string{"Feed"}
	if imageContext != "" {
		sources = append(sources, "Image")
	}
	if webContext != "" {
		sources = append(sources, "Web")
	}
	if a.callbacks.OnLog != nil {
		a.callbacks.OnLog(fmt.Sprintf("🔄 Combining sources: %s", strings.Join(sources, " + ")))
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

	userPrompt := fmt.Sprintf("Product Data:\n%s%s%s\n\nGenerate optimization proposals.", string(product.RawData), imageContext, webContext)

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
		
		// CRITICAL: Filter out "description-like" proposals (invented/placeholder values)
		if isDescriptionNotValue(p.After, p.Field) {
			if a.callbacks.OnLog != nil {
				a.callbacks.OnLog(fmt.Sprintf("⚠️ Filtered invalid proposal for %s: '%s' (not a concrete value)", p.Field, truncateString(p.After, 40)))
			}
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

// runFocusedMode runs optimization for a specific group with focused prompts
func (a *Agent) runFocusedMode(ctx context.Context, product *models.Product, group OptimizationGroup) ([]models.Proposal, error) {
	if a.callbacks.OnLog != nil {
		a.callbacks.OnLog(fmt.Sprintf("🎯 Running focused optimization: %s", group))
	}
	
	// Get group-specific context
	var imageContext string
	var webContext string
	
	// Only run image analysis for visual-related groups
	if group == GroupRecommendedAttrs || group == GroupImageAnalysis || group == GroupTitleOptimization {
		imageURL := extractImageURL(product.RawData)
		if imageURL != "" {
			imageContext = a.runImageAnalysisForGroup(ctx, imageURL, group)
		}
	}
	
	// Only run web search for specific groups
	if group == GroupRequiredAttributes || group == GroupRecommendedAttrs {
		webContext = a.runWebSearch(ctx, product)
	}
	
	// Get the group-specific prompt
	systemPrompt := getGroupPrompt(group)
	userPrompt := fmt.Sprintf("Product Data:\n%s%s%s\n\nGenerate optimization proposals for %s only.", 
		string(product.RawData), imageContext, webContext, group)
	
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
	
	a.recordUsage(ctx, openai.GPT4oMini, resp.Usage)
	
	// Parse response (same structure as runFastMode)
	var output struct {
		Score     float64 `json:"score"`
		Issues    []struct {
			Type        string  `json:"type"`
			Field       string  `json:"field"`
			Severity    string  `json:"severity"`
			Description string  `json:"description"`
		} `json:"issues,omitempty"`
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
	
	// Log issues if any
	if len(output.Issues) > 0 && a.callbacks.OnLog != nil {
		for _, issue := range output.Issues {
			a.callbacks.OnLog(fmt.Sprintf("⚠️ %s: %s - %s", issue.Severity, issue.Field, issue.Description))
		}
	}
	
	// Convert to models.Proposal
	var proposals []models.Proposal
	for _, p := range output.Proposals {
		if p.After == "" || p.After == p.Before {
			continue
		}
		if p.Confidence < 0.3 {
			continue
		}
		
		// CRITICAL: Filter out "description-like" proposals (invented/placeholder values)
		if isDescriptionNotValue(p.After, p.Field) {
			if a.callbacks.OnLog != nil {
				a.callbacks.OnLog(fmt.Sprintf("⚠️ Filtered invalid proposal for %s: '%s' (not a concrete value)", p.Field, truncateString(p.After, 40)))
			}
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
	
	if a.callbacks.OnLog != nil {
		a.callbacks.OnLog(fmt.Sprintf("✅ Generated %d proposals for %s", len(proposals), group))
	}
	
	return proposals, nil
}

// isDescriptionNotValue detects if a proposal value is a description/placeholder rather than actual data
func isDescriptionNotValue(value string, field string) bool {
	lower := strings.ToLower(value)
	
	// Common description patterns that should never be proposal values
	invalidPatterns := []string{
		"correct price",
		"valid product",
		"should be",
		"needs to be",
		"must be",
		"from landing page",
		"without watermarks",
		"recommended action",
		"needs update",
		"to be fixed",
		"requires review",
		"human review",
		"manual check",
		"verify this",
		"check the",
	}
	
	for _, pattern := range invalidPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	
	// For URL fields, must be actual URL
	urlFields := []string{"link", "image_link", "image", "url", "additional_image_link"}
	for _, urlField := range urlFields {
		if strings.Contains(strings.ToLower(field), urlField) {
			if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
				return true // URL fields must have actual URLs
			}
		}
	}
	
	// For price fields, must look like a price
	priceFields := []string{"price", "sale_price"}
	for _, priceField := range priceFields {
		if strings.ToLower(field) == priceField {
			// Should contain a number
			hasNumber := false
			for _, c := range value {
				if c >= '0' && c <= '9' {
					hasNumber = true
					break
				}
			}
			if !hasNumber {
				return true // Price fields must contain numbers
			}
		}
	}
	
	return false
}

// runImageAnalysisForGroup runs group-specific image analysis
func (a *Agent) runImageAnalysisForGroup(ctx context.Context, imageURL string, group OptimizationGroup) string {
	if a.callbacks.OnLog != nil {
		a.callbacks.OnLog("👁️ Analyzing product image...")
	}
	
	var prompt string
	switch group {
	case GroupImageAnalysis:
		prompt = `Analyze this product image for QUALITY and COMPLIANCE:
{
  "resolution": "estimated width x height",
  "aspect_ratio": "1:1 or other ratio",
  "background": "white/transparent/colored/lifestyle",
  "product_fill": "percentage of frame (ideal 75-90%)",
  "lighting": "professional/amateur/poor",
  "shadows": true/false,
  "watermarks": true/false,
  "text_overlay": true/false,
  "quality_score": 0-100,
  "issues": ["list of issues found"],
  "recommendations": ["suggested improvements"]
}`
	case GroupTitleOptimization, GroupRecommendedAttrs:
		prompt = `Extract visual attributes for product enrichment:
{
  "color": "main color(s)",
  "material": "visible material",
  "pattern": "pattern if any",
  "gender": "target gender if obvious",
  "product_type": "what type of product",
  "style": "style description"
}`
	default:
		return ""
	}
	
	imgResp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT4oMini,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleUser,
				MultiContent: []openai.ChatMessagePart{
					{Type: openai.ChatMessagePartTypeText, Text: prompt},
					{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: imageURL}},
				},
			},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
		MaxTokens:      300,
		Temperature:    0.1,
	})
	
	if err != nil {
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("❌ Image analysis failed: %v", err))
		}
		return ""
	}
	
	if len(imgResp.Choices) > 0 {
		a.recordUsage(ctx, openai.GPT4oMini, imgResp.Usage)
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("✅ Image analyzed"))
		}
		return "\n\n=== IMAGE ANALYSIS ===\n" + imgResp.Choices[0].Message.Content
	}
	return ""
}

// getGroupPrompt returns the system prompt for a specific optimization group
func getGroupPrompt(group OptimizationGroup) string {
	baseOutput := `

=== CRITICAL RULES - READ CAREFULLY ===

🚫 ABSOLUTE NO INVENTION RULE:
- NEVER propose a value you cannot provide concretely
- NEVER use descriptions like "correct price from landing page" or "valid image URL"
- The "after" field MUST contain an ACTUAL, CONCRETE value (a real URL, a real price, a real string)
- If you cannot determine a concrete replacement value, DO NOT create a proposal
- Instead, add an ISSUE to flag the problem for human review

✅ VALID proposal examples:
- "after": "Nike Air Max 90 Running Shoes Black" (concrete title)
- "after": "new" (concrete condition)
- "after": "male" (concrete gender)
- "after": "Apparel > Shoes > Running" (concrete category)

❌ INVALID proposal examples (NEVER DO THIS):
- "after": "valid product image URL without watermarks" ← WRONG! Not a real URL
- "after": "correct price from landing page" ← WRONG! Not a real price
- "after": "should be fixed" ← WRONG! Not a value
- "after": "needs update" ← WRONG! Not a value

When you CAN'T provide a concrete value:
→ Add to "issues" array instead of "proposals"
→ Let human review and fix manually

=== OUTPUT FORMAT (JSON) ===
{
  "score": 0.0-1.0,
  "issues": [{"type": "error|warning", "field": "field_name", "severity": "critical|high|medium|low", "description": "Issue description for human review"}],
  "proposals": [
    {
      "field": "field_name",
      "before": "current value",
      "after": "CONCRETE NEW VALUE - must be real, usable data",
      "rationale": "why this change",
      "source": "feed|image|web|inferred",
      "confidence": 0.0-1.0,
      "risk_level": "low|medium|high"
    }
  ]
}

REMEMBER: If you cannot write a real, concrete value in "after", DO NOT create a proposal.`

	switch group {
	case GroupCriticalErrors:
		return `You are a GMC Feed Auditor specialized in CRITICAL ERRORS detection.

IMPORTANT: This audit is primarily for DETECTION, not correction.
- Most critical errors require HUMAN action (fix at source, verify landing page, etc.)
- Only create a proposal if you have a CONCRETE fix (e.g., fixing GTIN checksum, formatting price)
- For issues you detect but cannot fix with concrete data, add to "issues" array

Focus ONLY on detecting these issues:

🚫 POLICY VIOLATIONS
- Prohibited products, claims, or content
- → Add to issues, cannot auto-fix

💰 PRICE MISMATCH
- price field format issues (missing currency, wrong format)
- sale_price > price (error)
- → Only propose if you can fix FORMAT (e.g., "29.99" → "29.99 EUR")
- → Cannot know the "correct" price - add to issues for human review

📦 AVAILABILITY MISMATCH  
- Invalid availability value
- → Can propose valid values: in_stock, out_of_stock, preorder, backorder

🔗 INVALID URLs
- Malformed URLs (missing http/https, invalid characters)
- → Only propose if you can fix the format
- → Cannot verify if URL works - add to issues

🏷️ INVALID GTIN
- Wrong number of digits
- Invalid checksum
- Placeholder values (0000000000000)
- → Add to issues, cannot invent valid GTIN

🖼️ IMAGE POLICY VIOLATIONS
- URL format issues
- → Cannot verify image content - add to issues for human review
` + baseOutput

	case GroupRequiredAttributes:
		return `You are a GMC Feed Auditor checking REQUIRED ATTRIBUTES COMPLETENESS.

⚠️ SCOPE: Check EXISTENCE and VALIDITY only - DO NOT optimize content.
If a field exists and is valid, leave it alone. Content optimization is a separate group.

What you CAN propose:
✅ condition: "new" if field is EMPTY (default value)
✅ brand: Fix CAPITALIZATION only if brand exists (e.g., "nike" → "Nike")
✅ title: ONLY if EMPTY or too short (<10 chars) - add basic info from other fields
✅ description: ONLY if EMPTY or too short (<50 chars)

What you CANNOT do:
❌ DO NOT rewrite existing titles - that's Title Optimization group
❌ DO NOT enhance existing descriptions - that's Description Optimization group
❌ DO NOT invent brand, gtin, mpn
❌ DO NOT change id

VALIDATION CHECKS (add to issues if failed):
🆔 ID - Must exist, max 50 chars
📝 TITLE - Must exist, 1-150 chars
📄 DESCRIPTION - Must exist, 1-5000 chars
🏷️ BRAND - Should exist for most categories
🔢 GTIN/MPN - Should exist (or identifier_exists=false)
✨ CONDITION - Must be: new, refurbished, or used

ONLY create proposals for MISSING or INVALID fields, not for optimization.
` + baseOutput

	case GroupRecommendedAttrs:
		return `You are a GMC Feed Auditor specialized in ADDING MISSING RECOMMENDED ATTRIBUTES.

⚠️ SCOPE: Only ADD attributes that are EMPTY or MISSING.
If a field already has a value, DO NOT modify it.

What you CAN propose (only for EMPTY fields):
✅ color: From image analysis - only if field is empty
✅ material: From image or inferred - only if field is empty
✅ pattern: From image - only if field is empty
✅ gender: From context - only if field is empty (male/female/unisex)
✅ age_group: "adult" as default - only if field is empty
✅ size_system: Infer from currency/domain - only if field is empty
✅ product_type: Build from title - only if field is empty
✅ google_product_category: Map to taxonomy - only if field is empty

CRITICAL RULES:
❌ If color="blue" exists → DO NOT propose a different color
❌ If gender="female" exists → DO NOT change it
❌ If product_type exists → DO NOT modify it
❌ Only propose for EMPTY/MISSING fields

CHECK THESE FIELDS:
📂 google_product_category - Empty? → Propose taxonomy mapping
🗂️ product_type - Empty? → Build hierarchy from title
🎨 color - Empty? → Extract from image
📏 size - Empty? → Extract from title if present
🧵 material - Empty? → From image or description
👤 gender - Empty? → Infer from product type
👶 age_group - Empty? → Default "adult"
👥 item_group_id - Empty? → Flag in issues (cannot invent)
` + baseOutput

	case GroupTitleOptimization:
		return `You are a GMC Title Optimizer. THIS is the group that OPTIMIZES title content.

⚠️ SCOPE: Improve EXISTING titles by restructuring and enriching.
Use data from feed fields AND image analysis to build better titles.

=== TITLE TEMPLATES BY CATEGORY ===

👕 APPAREL: Brand + Gender + Type + Color + Size + Material
   → "Nike Men's Air Max 90 Running Shoes Black Size 42 Leather"

📱 ELECTRONICS: Brand + Line + Model + Key Spec + Capacity
   → "Samsung Galaxy S24 Ultra 5G Smartphone 256GB Titanium Gray"

🏠 HOME & GARDEN: Brand + Type + Material + Dimensions + Style
   → "IKEA KALLAX Shelf Unit Engineered Wood White 77x147cm Modern"

💄 BEAUTY: Brand + Line + Type + Variant + Size
   → "L'Oréal Paris Revitalift Night Cream Anti-Wrinkle 50ml"

=== WHAT TO DO ===
✅ Restructure title using template for the category
✅ ADD attributes from other feed fields (brand, color, size, material)
✅ ADD attributes from image analysis (color, material, pattern)
✅ Front-load keywords (first 70 chars visible in search)
✅ Optimal length: 70-100 chars, max 150 chars

=== WHAT NOT TO DO ===
❌ NO promotional text (SALE, FREE SHIPPING, -50%)
❌ NO ALL CAPS (except brand names if official)
❌ NO keyword stuffing (repeating same words)
❌ NO special symbols (★ ♥ → ●)
❌ NO invented information - only use data from feed + image

ALWAYS propose an improved title if the current one can be better structured.
` + baseOutput

	case GroupDescOptimization:
		return `You are a GMC Description Optimizer. THIS is the group that OPTIMIZES description content.

⚠️ SCOPE: Improve EXISTING descriptions by enriching and restructuring.
Use data from feed fields AND image analysis to build better descriptions.

=== DESCRIPTION STRUCTURE ===

1️⃣ ACCROCHE (1-2 sentences)
   - Main benefit or value proposition
   - Hook the reader

2️⃣ FEATURES (3-5 bullet points equivalent)
   - Key characteristics from feed data
   - Visual details from image analysis

3️⃣ SPECS (technical details)
   - Dimensions, materials, capacity
   - Compatibility info

4️⃣ USE CASES (optional)
   - Who is it for
   - When/where to use it

=== WHAT TO DO ===
✅ Restructure description with clear sections
✅ ADD details from other feed fields
✅ ADD visual observations from image analysis (color, material, style)
✅ Minimum 100 chars, ideal 500-1000 chars
✅ Include product_highlight if empty (4-6 key features)

=== WHAT NOT TO DO ===
❌ NO HTML tags
❌ NO price or availability info
❌ NO promotional text (sale, discount, free shipping)
❌ NO links or references to other sites
❌ NO ALL CAPS sections
❌ NO invented information - only use data from feed + image

ALWAYS propose an improved description if the current one can be enhanced.
` + baseOutput

	case GroupImageAnalysis:
		return `You are a GMC Image Quality Auditor. Analyze image compliance and quality.

⚠️ IMPORTANT: This audit is for DETECTION ONLY - DO NOT CREATE PROPOSALS.
You CANNOT provide replacement image URLs, so:
- Report ALL findings in the "issues" array
- Leave "proposals" array EMPTY
- Issues will be flagged for human review

=== IMAGE REQUIREMENTS TO CHECK ===

📐 TECHNICAL SPECS
- Minimum: 800x800 pixels
- Recommended: 1200x1200 or higher
- Max file size: 16MB
- Formats: JPEG, PNG, GIF, WebP

📷 PRIMARY IMAGE RULES
- Product on white or transparent background
- Product fills 75-90% of frame
- Professional lighting, minimal shadows
- No lifestyle/context elements for primary image

🚫 PROHIBITED ELEMENTS
- Watermarks or logos overlaid
- Promotional text or badges
- Borders or frames
- Placeholder images
- Multiple products (unless bundle)

=== OUTPUT FORMAT ===
{
  "score": 0.0-1.0,
  "issues": [
    {"type": "error|warning", "field": "image_link", "severity": "critical|high|medium|low", "description": "Detailed issue description"}
  ],
  "proposals": []
}

NOTE: proposals MUST be empty - you cannot invent image URLs. All findings go to issues.`

	case GroupPricingPromotions:
		return `You are a GMC Pricing & Promotions Auditor. Focus ONLY on price-related fields.

⚠️ IMPORTANT - WHAT YOU CAN AND CANNOT DO:

✅ CAN PROPOSE (format fixes only):
- Add missing currency: "29.99" → "29.99 EUR"
- Fix format: "29,99 EUR" → "29.99 EUR"
- Remove sale_price if sale_price > price

❌ CANNOT PROPOSE (add to issues instead):
- The "correct" price (you don't know it)
- Price from landing page (you can't scrape it)
- Whether price matches landing page (flag as issue for human)

=== VALIDATION RULES ===

💰 PRICE FORMAT
- Valid format: "29.99 EUR" or "USD 29.99"
- Must include currency code (EUR, USD, GBP, etc.)
- Use decimal point, not comma
- If price is "29.99" without currency → propose "29.99 EUR" (infer from context)

💸 SALE_PRICE LOGIC
- If sale_price > price → add to issues (this is an error)
- If sale_price exists, it must have same currency as price

📅 DATE FORMAT
- ISO 8601: 2024-01-15T00:00:00+01:00/2024-01-31T23:59:59+01:00

=== OUTPUT ===
- Propose FORMAT fixes only (adding currency, fixing decimal)
- Add to issues: price mismatches, invalid sale prices, expired dates
` + baseOutput

	default:
		return `You are a GMC product data optimizer. Analyze and generate optimization proposals.` + baseOutput
	}
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

// runWebSearch searches for product info using Brave Search API
func (a *Agent) runWebSearch(ctx context.Context, product *models.Product) string {
	// Check if Brave API key is configured
	if a.config.WebSearch.APIKey == "" {
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog("⚠️ Brave API key not configured - skipping web search")
		}
		return ""
	}
	
	// Extract search query from product data
	var fields map[string]interface{}
	json.Unmarshal(product.RawData, &fields)
	
	// Priority 1: GTIN/EAN (most precise)
	gtin := getFieldValueFromMap(fields, "gtin")
	if gtin == "" {
		gtin = getFieldValueFromMap(fields, "ean")
	}
	if gtin == "" {
		gtin = getFieldValueFromMap(fields, "upc")
	}
	
	// Priority 2: Brand + Title
	brand := getFieldValueFromMap(fields, "brand")
	if brand == "" {
		brand = getFieldValueFromMap(fields, "marque")
	}
	title := getFieldValueFromMap(fields, "title")
	if title == "" {
		title = getFieldValueFromMap(fields, "titre")
	}
	if title == "" {
		title = getFieldValueFromMap(fields, "nom")
	}
	
	// Build search query
	var query string
	if gtin != "" && len(gtin) >= 8 {
		query = gtin + " product specifications"
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("🔍 Web search: GTIN %s", gtin))
		}
	} else if brand != "" && title != "" {
		query = brand + " " + title
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("🔍 Web search: %s %s", brand, truncateString(title, 30)))
		}
	} else if title != "" {
		query = title
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("🔍 Web search: %s", truncateString(title, 40)))
		}
	} else {
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog("⚠️ No searchable product info - skipping web search")
		}
		return ""
	}
	
	// Call Brave Search API
	searchURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=3&extra_snippets=true",
		url.QueryEscape(query))
	
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("❌ Web search request error: %v", err))
		}
		return ""
	}
	
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", a.config.WebSearch.APIKey)
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("❌ Web search failed: %v", err))
		}
		return ""
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("❌ Brave API error %d: %s", resp.StatusCode, truncateString(string(body), 100)))
		}
		return ""
	}
	
	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog(fmt.Sprintf("❌ Parse Brave response: %v", err))
		}
		return ""
	}
	
	if len(braveResp.Web.Results) == 0 {
		if a.callbacks.OnLog != nil {
			a.callbacks.OnLog("⚠️ No web results found")
		}
		return ""
	}
	
	// Build web context
	var webResults []string
	for i, r := range braveResp.Web.Results {
		if i >= 3 {
			break
		}
		webResults = append(webResults, fmt.Sprintf("- %s\n  %s\n  Source: %s", r.Title, r.Description, r.URL))
	}
	
	if a.callbacks.OnLog != nil {
		a.callbacks.OnLog(fmt.Sprintf("✅ Web: Found %d results", len(webResults)))
	}
	
	return "\n\n=== WEB SEARCH RESULTS ===\n" + strings.Join(webResults, "\n\n")
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
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
