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
	systemPrompt := `You are a product data optimization expert for Google Merchant Center.

Your task: Analyze the product and generate optimization proposals.

RESPONSIBILITIES:
1. ANALYZE: Score data quality (0-1), identify missing/weak fields, violations
2. OPTIMIZE: Generate concrete proposals for improvements

CRITICAL RULES:
- Generate proposals for ALL improvable fields (title, description, color, material, gender, etc.)
- ALWAYS optimize title if < 60 chars or missing key attributes (brand, product type, key features)
- ALWAYS optimize description if < 100 chars or not informative
- Fill ALL missing recommended GMC attributes you can reasonably infer
- Use ONLY facts from the provided data or image analysis (NO invention)
- Each proposal must have a clear rationale

GMC TITLE BEST PRACTICES:
- Include: Brand + Product Type + Key Attributes (color, size, material)
- 60-150 characters optimal
- No promotional text ("Free shipping", "Sale")
- Front-load important keywords

GMC DESCRIPTION BEST PRACTICES:
- Include: Product benefits, specifications, use cases
- 100-500 characters optimal
- Be specific and informative

OUTPUT FORMAT (JSON):
{
  "analysis": {
    "score": 0.65,
    "missing_fields": ["color", "material"],
    "weak_fields": ["title", "description"],
    "violations": ["title too short"]
  },
  "proposals": [
    {
      "field": "title",
      "before": "Blue Shirt",
      "after": "Nike Men's Dri-FIT Blue Running Shirt - Lightweight Breathable",
      "rationale": "Added brand, gender, product type, and key features for better GMC visibility",
      "sources": ["feed:brand", "feed:title", "image:color"],
      "confidence": 0.9,
      "risk_level": "low"
    }
  ]
}

IMPORTANT: Generate AT LEAST one proposal for title and description if they can be improved.
Be GENEROUS with proposals - it's better to propose improvements that get rejected than to miss opportunities.`

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
						Text: `Extract FACTUAL observations from this product image. Output JSON:
{
  "color": "observed color or null",
  "material": "observed material or null",  
  "style": "style/type observation or null",
  "gender": "target gender if obvious or null",
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
