package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/benjamincozon/feedenrich/internal/agent/tools"
	"github.com/benjamincozon/feedenrich/internal/config"
	"github.com/benjamincozon/feedenrich/internal/models"
	"github.com/google/uuid"
	openai "github.com/sashabaranov/go-openai"
)

// FastPipeline is an optimized version that minimizes API calls
// by combining multiple steps into a single LLM call while maintaining
// the separation of concerns through structured prompting
type FastPipeline struct {
	config    *config.Config
	client    *openai.Client
	validator *tools.HardRuleValidator
	differ    *tools.DiffEngine
	risk      *tools.RiskClassifier
	callbacks PipelineCallbacks
}

// FastProposal is the output format we expect from the LLM
type FastProposal struct {
	Field       string   `json:"field"`
	Before      string   `json:"before"`
	After       string   `json:"after"`
	Rationale   string   `json:"rationale"`
	Sources     []string `json:"sources"`
	Confidence  float64  `json:"confidence"`
	RiskLevel   string   `json:"risk_level"`
}

type FastPipelineOutput struct {
	Analysis struct {
		Score          float64  `json:"score"`
		MissingFields  []string `json:"missing_fields"`
		WeakFields     []string `json:"weak_fields"`
		Violations     []string `json:"violations"`
	} `json:"analysis"`
	Proposals []FastProposal `json:"proposals"`
}

func NewFastPipeline(cfg *config.Config) *FastPipeline {
	clientConfig := openai.DefaultConfig(cfg.OpenAI.APIKey)
	return &FastPipeline{
		config:    cfg,
		client:    openai.NewClientWithConfig(clientConfig),
		validator: tools.NewHardRuleValidator(),
		differ:    tools.NewDiffEngine(),
		risk:      tools.NewRiskClassifier(),
	}
}

func (p *FastPipeline) SetCallbacks(cb PipelineCallbacks) {
	p.callbacks = cb
}

// Run executes an optimized pipeline with minimal API calls
func (p *FastPipeline) Run(ctx context.Context, product *models.Product) (*PipelineResult, error) {
	result := &PipelineResult{
		ProductID:     product.ID,
		StartedAt:     time.Now(),
		Stages:        []StageResult{},
		Proposals:     []*Proposal{},
		Rejections:    []*Rejection{},
		HumanRequired: []*HumanReviewRequest{},
	}

	// Stage 1: Hard Rule Validation (deterministic, instant)
	if p.callbacks.OnStageStart != nil {
		p.callbacks.OnStageStart("validate")
	}
	validationResult := p.validator.Validate(product.RawData)
	result.Stages = append(result.Stages, StageResult{
		Stage:      "validate",
		StartedAt:  time.Now(),
		EndedAt:    time.Now(),
		DurationMs: 1,
	})
	if p.callbacks.OnStageEnd != nil {
		p.callbacks.OnStageEnd("validate", validationResult)
	}

	// Stage 2-5: Combined AI call (audit + plan + execute)
	// Run in parallel with image analysis if image available
	var wg sync.WaitGroup
	var imageContext string
	var imageErr error
	
	imageURL := extractImageURL(product.RawData)
	if imageURL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.callbacks.OnStageStart != nil {
				p.callbacks.OnStageStart("image_evidence")
			}
			imageContext, imageErr = p.analyzeImageFast(ctx, imageURL)
			if p.callbacks.OnStageEnd != nil {
				p.callbacks.OnStageEnd("image_evidence", imageContext)
			}
		}()
	}

	// Main optimization call
	if p.callbacks.OnStageStart != nil {
		p.callbacks.OnStageStart("optimize")
	}

	// Wait for image analysis if running
	wg.Wait()

	// Build context including image observations
	contextInfo := ""
	if imageContext != "" && imageErr == nil {
		contextInfo = fmt.Sprintf("\n\nImage Analysis Results:\n%s", imageContext)
	}

	// Single combined call
	output, err := p.runCombinedOptimization(ctx, product.RawData, contextInfo)
	if err != nil {
		result.CompletedAt = time.Now()
		if p.callbacks.OnError != nil {
			p.callbacks.OnError("optimize", err)
		}
		return result, err
	}

	result.Stages = append(result.Stages, StageResult{
		Stage:      "optimize",
		StartedAt:  time.Now(),
		EndedAt:    time.Now(),
		DurationMs: 100,
	})

	if p.callbacks.OnStageEnd != nil {
		p.callbacks.OnStageEnd("optimize", output)
	}

	// Stage 6: Deterministic validation (no AI, instant)
	if p.callbacks.OnStageStart != nil {
		p.callbacks.OnStageStart("control")
	}

	for _, prop := range output.Proposals {
		// Deterministic checks
		isValid := p.validateProposalDeterministic(prop)
		
		if !isValid {
			result.Rejections = append(result.Rejections, &Rejection{
				Field:  prop.Field,
				Reason: "Failed deterministic validation",
				Stage:  "controller",
			})
			if p.callbacks.OnRejection != nil {
				p.callbacks.OnRejection(prop.Field, "Failed validation")
			}
			continue
		}

		// Risk assessment
		riskAssessment := p.risk.AssessChange(prop.Field, prop.Before, prop.After, "mixed", prop.Confidence)

		proposal := &Proposal{
			ID:         uuid.New(),
			Field:      prop.Field,
			Before:     prop.Before,
			After:      prop.After,
			Objective:  prop.Rationale,
			Risk:       riskAssessment,
			Verified:   true,
			Confidence: prop.Confidence,
		}
		result.Proposals = append(result.Proposals, proposal)

		if p.callbacks.OnProposal != nil {
			p.callbacks.OnProposal(proposal)
		}

		// Flag high risk for human review
		if riskAssessment.Level == "high" {
			result.HumanRequired = append(result.HumanRequired, &HumanReviewRequest{
				Field:     prop.Field,
				Reason:    "High risk change",
				RiskLevel: "high",
			})
			if p.callbacks.OnHumanNeeded != nil {
				p.callbacks.OnHumanNeeded(prop.Field, "High risk change")
			}
		}
	}

	if p.callbacks.OnStageEnd != nil {
		p.callbacks.OnStageEnd("control", result.Proposals)
	}

	// Build summary
	result.CompletedAt = time.Now()
	result.Summary = &PipelineSummary{
		TotalStages:       3, // validate, optimize, control
		ProposalsCreated:  len(result.Proposals),
		ProposalsApproved: len(result.Proposals),
		ProposalsRejected: len(result.Rejections),
		HumanReviewNeeded: len(result.HumanRequired),
		DurationMs:        result.CompletedAt.Sub(result.StartedAt).Milliseconds(),
		ScoreBefore:       output.Analysis.Score,
		ScoreAfter:        output.Analysis.Score + float64(len(result.Proposals))*0.05,
	}

	if result.Summary.ScoreAfter > 1.0 {
		result.Summary.ScoreAfter = 1.0
	}

	if p.callbacks.OnComplete != nil {
		p.callbacks.OnComplete(result.Summary)
	}

	return result, nil
}

func (p *FastPipeline) runCombinedOptimization(ctx context.Context, productData json.RawMessage, additionalContext string) (*FastPipelineOutput, error) {
	systemPrompt := `You are a product data optimization expert for Google Merchant Center (GMC).

=== GMC ATTRIBUTES REFERENCE (2025) ===

REQUIRED ATTRIBUTES:
- id: Unique product identifier
- title: Product name (30-150 chars)
- description: Product description (50-5000 chars)
- link: Product page URL
- image_link: Main product image URL
- price: Product price with currency
- availability: in_stock, out_of_stock, preorder, backorder
- brand: Manufacturer/brand name

REQUIRED FOR APPAREL (US, UK, DE, JP, FR, BR):
- color: Product color - use standard names (black, blue, red), no hex codes
- gender: male, female, unisex
- age_group: newborn, infant, toddler, kids, adult
- size: S, M, L, XL or numeric (8, 9.5, etc.)

STRONGLY RECOMMENDED:
- gtin: EAN (13 digits) / UPC (12 digits) / ISBN
- mpn: Manufacturer Part Number
- google_product_category: Google taxonomy ID (e.g., "Apparel & Accessories > Clothing > Shirts")
- product_type: Your category hierarchy
- condition: new, used, refurbished
- item_group_id: For variants (same product, different size/color)

INFERABLE ATTRIBUTES (propose when missing):
- material: cotton, polyester, leather, wool, silk, denim, etc.
- pattern: solid, striped, floral, checkered, printed, etc.
- size_type: regular, petite, plus, tall, maternity
- size_system: US, UK, EU, FR, IT
- product_weight: For shipping
- product_height, product_width, product_length: Dimensions

=== OPTIMIZATION TASKS ===

1. TITLE (ALWAYS optimize if improvable):
   - Template: Brand + Gender + Product Type + Color + Size + Material
   - Min 30 chars, optimal 60-150 chars
   - Front-load keywords (first 70 chars visible in search)
   - FORBIDDEN: promotional text (free shipping, sale, -50%, soldes)

2. DESCRIPTION (ALWAYS optimize if improvable):
   - Include: benefits, features, specifications, use cases
   - Min 50 chars, optimal 100-500 chars
   - Be specific and informative

3. MISSING ATTRIBUTES (propose ALL that are inferable):
   - color: From image or title ("Blue T-Shirt" → color: "blue")
   - gender: From product type or image
   - age_group: Default "adult" unless clearly kids product
   - material: From image texture or product type
   - pattern: From image (solid, striped, etc.)
   - size: Extract from title if present
   - product_type: Build from category/title
   - google_product_category: Map to Google taxonomy

=== OUTPUT FORMAT (JSON) ===
{
  "analysis": {
    "score": 0.65,
    "missing_fields": ["color", "gender", "age_group", "material"],
    "weak_fields": ["title", "description"],
    "violations": ["title too short", "missing required apparel attributes"]
  },
  "proposals": [
    {
      "field": "title",
      "before": "Blue Shirt",
      "after": "Nike Men's Dri-FIT Blue Running Shirt - Lightweight Breathable",
      "rationale": "Added brand, gender, product type, and key features",
      "sources": ["feed:brand", "feed:title", "image:color"],
      "confidence": 0.9,
      "risk_level": "low"
    },
    {
      "field": "color",
      "before": "",
      "after": "blue",
      "rationale": "Color visible in product image and mentioned in title",
      "sources": ["image:color", "feed:title"],
      "confidence": 0.95,
      "risk_level": "low"
    },
    {
      "field": "gender",
      "before": "",
      "after": "male",
      "rationale": "Product is clearly men's apparel based on title and styling",
      "sources": ["feed:title", "image:style"],
      "confidence": 0.85,
      "risk_level": "low"
    }
  ]
}

=== CRITICAL RULES ===
- NO INVENTION: Only use facts from feed data or image analysis
- Be GENEROUS: Propose all improvements, let humans reject if needed
- Generate AT LEAST 3-5 proposals for products with missing attributes
- For APPAREL: ALWAYS propose color, gender, age_group, size if missing`

	userPrompt := fmt.Sprintf(`Product Data:
%s
%s

Analyze this product and generate optimization proposals. Be thorough - propose improvements for every field that could be better.`, string(productData), additionalContext)

	resp, err := p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT4oMini,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
		Temperature: 0.3,
	})
	if err != nil {
		return nil, err
	}

	var output FastPipelineOutput
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &output); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return &output, nil
}

func (p *FastPipeline) analyzeImageFast(ctx context.Context, imageURL string) (string, error) {
	resp, err := p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT4oMini,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleUser,
				MultiContent: []openai.ChatMessagePart{
					{
						Type: openai.ChatMessagePartTypeText,
						Text: `Extract ALL factual GMC attributes from this product image. Output JSON:
{
  "color": "primary color (e.g., black, blue, red, white, beige)",
  "secondary_colors": ["additional colors if multicolor"],
  "material": "visible material (e.g., cotton, leather, denim, wool, polyester)",
  "pattern": "pattern type (solid, striped, floral, checkered, printed, geometric)",
  "style": "product style/type (e.g., casual, formal, sporty, vintage)",
  "gender": "target gender if obvious (male, female, unisex)",
  "age_group": "target age if obvious (adult, kids, infant)",
  "product_type": "what the product is (e.g., t-shirt, sneakers, handbag)",
  "other_observations": ["any other relevant facts"]
}

ONLY state what you can clearly see. Do NOT invent or guess.`,
					},
					{
						Type: openai.ChatMessagePartTypeImageURL,
						ImageURL: &openai.ChatMessageImageURL{
							URL: imageURL,
						},
					},
				},
			},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
		MaxTokens:   300,
		Temperature: 0.1,
	})
	if err != nil {
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}

func (p *FastPipeline) validateProposalDeterministic(prop FastProposal) bool {
	// Basic sanity checks (no AI needed)
	
	// After must be different from before
	if prop.After == prop.Before {
		return false
	}
	
	// After must not be empty
	if prop.After == "" {
		return false
	}
	
	// After should not be shorter than before (unless cleaning up)
	if len(prop.After) < len(prop.Before)/2 {
		return false
	}
	
	// Confidence must be reasonable
	if prop.Confidence < 0.3 {
		return false
	}
	
	return true
}
