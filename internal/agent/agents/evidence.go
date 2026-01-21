package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// ImageEvidenceAgent extracts visual evidence from product images.
// It is FORBIDDEN to infer meaning.
// Allowed: detect, confirm, deny, mark uncertainty
// NO adjectives, NO marketing language - EVIDENCE ONLY
type ImageEvidenceAgent struct {
	client *openai.Client
	config *config.Config
}

func NewImageEvidenceAgent(cfg *config.Config) *ImageEvidenceAgent {
	return &ImageEvidenceAgent{
		client: openai.NewClient(cfg.OpenAI.APIKey),
		config: cfg,
	}
}

// ImageEvidenceInput contains the image URL and optional attributes to verify
type ImageEvidenceInput struct {
	ImageURL           string   `json:"image_url"`
	AttributesToVerify []string `json:"attributes_to_verify,omitempty"` // e.g., ["color", "material", "has_pockets"]
}

// ImageEvidenceOutput contains only factual observations
// NO adjectives, NO marketing language - EVIDENCE ONLY
type ImageEvidenceOutput struct {
	Observations []ImageObservation `json:"observations"`
	Uncertain    []string           `json:"uncertain"` // attributes that couldn't be determined
	ImageQuality ImageQualityCheck  `json:"image_quality"`
}

type ImageObservation struct {
	Attribute  string  `json:"attribute"`
	Value      any     `json:"value"` // string or bool
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"` // factual reasoning only
}

type ImageQualityCheck struct {
	IsProductImage bool    `json:"is_product_image"`
	IsClear        bool    `json:"is_clear"`
	HasWatermark   bool    `json:"has_watermark"`
	HasText        bool    `json:"has_text_overlay"`
	BackgroundType string  `json:"background_type"` // white, transparent, lifestyle, other
	Confidence     float64 `json:"confidence"`
}

// ExtractEvidence analyzes an image and returns factual observations
func (a *ImageEvidenceAgent) ExtractEvidence(ctx context.Context, input ImageEvidenceInput) (*ImageEvidenceOutput, error) {
	if !a.config.Agent.EnableVision {
		return &ImageEvidenceOutput{
			Uncertain: []string{"vision_disabled"},
		}, nil
	}

	attributesHint := ""
	if len(input.AttributesToVerify) > 0 {
		attrsJSON, _ := json.Marshal(input.AttributesToVerify)
		attributesHint = fmt.Sprintf("\n\nATTRIBUTES TO VERIFY: %s", string(attrsJSON))
	}

	prompt := fmt.Sprintf(`You are an IMAGE EVIDENCE AGENT. Your role is to extract FACTUAL observations from product images.

CRITICAL CONSTRAINTS:
- You are FORBIDDEN to infer meaning
- You can ONLY: detect, confirm, deny, mark uncertainty
- NO adjectives (don't say "beautiful", "elegant", "high-quality")
- NO marketing language
- EVIDENCE ONLY

ALLOWED OBSERVATIONS:
- Colors (factual: "black", "red", "blue" - not "stunning black")
- Physical attributes (has_pockets: true/false, has_collar: true/false)
- Product type indicators (appears to be a shirt, appears to be electronics)
- Visible text/labels
- Image quality issues

FORBIDDEN:
- Quality judgments ("premium", "well-made")
- Material inference (unless clearly labeled in image)
- Size inference
- Price/value inference
- Style judgments ("fashionable", "modern")
%s

OUTPUT FORMAT (JSON only):
{
  "observations": [
    { "attribute": "color", "value": "black", "confidence": 0.92, "reasoning": "primary visible color of the product" },
    { "attribute": "has_pockets", "value": true, "confidence": 0.88, "reasoning": "visible pocket outlines on front" }
  ],
  "uncertain": ["material", "lining"],
  "image_quality": {
    "is_product_image": true,
    "is_clear": true,
    "has_watermark": false,
    "has_text_overlay": false,
    "background_type": "white",
    "confidence": 0.95
  }
}

Return ONLY the JSON, no explanations.`, attributesHint)

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.config.OpenAI.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleUser,
				MultiContent: []openai.ChatMessagePart{
					{Type: openai.ChatMessagePartTypeText, Text: prompt},
					{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: input.ImageURL}},
				},
			},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("image evidence call failed: %w", err)
	}

	var output ImageEvidenceOutput
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &output); err != nil {
		return nil, fmt.Errorf("parse image evidence output: %w", err)
	}

	return &output, nil
}
