package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/config"
	"github.com/benjamincozon/feedenrich/internal/models"
	openai "github.com/sashabaranov/go-openai"
)

// AnalyzeProductTool analyzes the current state of a product
type AnalyzeProductTool struct {
	client *openai.Client
	config *config.Config
}

func (t *AnalyzeProductTool) Name() string { return "analyze_product" }

func (t *AnalyzeProductTool) Description() string {
	return "Analyze the current state of the product, identify gaps, compliance issues, and score quality"
}

func (t *AnalyzeProductTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"product_id": map[string]any{
				"type":        "string",
				"description": "The product ID to analyze (optional, uses current product if not specified)",
			},
		},
		"required": []string{},
	}
}

func (t *AnalyzeProductTool) Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error) {
	productData := session.GetProductData()

	// Use GPT to analyze the product
	prompt := fmt.Sprintf(`Analyse ce produit et retourne un JSON avec:
- gmc_compliance: { valid: bool, errors: [{ field, issue, severity }] }
- quality_scores: { title_quality, description_quality, completeness, agent_readiness } (0-1)
- missing_attributes: liste des attributs manquants importants
- improvement_opportunities: [{ field, current_issue, potential_action }]

Produit:
%s

Règles GMC à vérifier:
- title: min 30 chars, max 150, doit contenir marque/type/caractéristiques clés
- description: min 50 chars, informatif
- image_link: requis, URL valide
- price: requis, format correct
- brand: recommandé
- gtin ou mpn: au moins un requis
- color, gender, size: recommandés pour vêtements/chaussures

Retourne UNIQUEMENT le JSON, sans markdown.`, string(productData))

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
		return nil, fmt.Errorf("openai analyze: %w", err)
	}

	var result models.AnalysisResult
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		// Return raw response if parsing fails
		return json.RawMessage(resp.Choices[0].Message.Content), nil
	}

	return result, nil
}

// AnalyzeImageTool uses vision to analyze product images
type AnalyzeImageTool struct {
	client *openai.Client
	config *config.Config
}

func (t *AnalyzeImageTool) Name() string { return "analyze_image" }

func (t *AnalyzeImageTool) Description() string {
	return "Analyze product images to extract visual attributes (color, style, features). Only for low-risk observations."
}

func (t *AnalyzeImageTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"image_url": map[string]any{
				"type":        "string",
				"description": "URL of the image to analyze",
			},
			"questions": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Specific questions to answer about the image",
			},
		},
		"required": []string{"image_url"},
	}
}

type AnalyzeImageInput struct {
	ImageURL  string   `json:"image_url"`
	Questions []string `json:"questions"`
}

type ImageObservation struct {
	Attribute  string  `json:"attribute"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

type AnalyzeImageOutput struct {
	Observations []ImageObservation `json:"observations"`
	Warnings     []string           `json:"warnings"`
}

func (t *AnalyzeImageTool) Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error) {
	var params AnalyzeImageInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	if !t.config.Agent.EnableVision {
		return AnalyzeImageOutput{
			Warnings: []string{"Vision is disabled"},
		}, nil
	}

	questionsPrompt := ""
	if len(params.Questions) > 0 {
		questionsPrompt = "\n\nQuestions spécifiques:\n"
		for _, q := range params.Questions {
			questionsPrompt += "- " + q + "\n"
		}
	}

	prompt := fmt.Sprintf(`Analyse cette image produit et identifie les attributs visuels observables.

RÈGLES:
- Ne rapporte QUE ce qui est clairement visible
- N'invente JAMAIS de caractéristiques techniques (matière, composition, etc.)
- Donne un score de confiance honnête (0-1)
- Si l'image est floue ou ambiguë, dis-le

Retourne un JSON avec:
{
  "observations": [{ "attribute": "...", "value": "...", "confidence": 0.X, "reasoning": "..." }],
  "warnings": ["..."]
}%s

Retourne UNIQUEMENT le JSON.`, questionsPrompt)

	resp, err := t.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: t.config.OpenAI.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleUser,
				MultiContent: []openai.ChatMessagePart{
					{Type: openai.ChatMessagePartTypeText, Text: prompt},
					{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: params.ImageURL}},
				},
			},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai vision: %w", err)
	}

	var result AnalyzeImageOutput
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return json.RawMessage(resp.Choices[0].Message.Content), nil
	}

	return result, nil
}
