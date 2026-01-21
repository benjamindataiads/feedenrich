package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// OptimizeFieldTool generates improved versions of product fields
type OptimizeFieldTool struct {
	client *openai.Client
	config *config.Config
}

func (t *OptimizeFieldTool) Name() string { return "optimize_field" }

func (t *OptimizeFieldTool) Description() string {
	return "Generate an optimized version of a product field (title or description) using gathered facts"
}

func (t *OptimizeFieldTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"field": map[string]any{
				"type":        "string",
				"enum":        []string{"title", "description"},
				"description": "The field to optimize",
			},
			"current_value": map[string]any{
				"type":        "string",
				"description": "Current value of the field",
			},
			"context": map[string]any{
				"type":        "object",
				"description": "Context information (brand, category, attributes, gathered_facts)",
			},
			"constraints": map[string]any{
				"type":        "object",
				"description": "Constraints (max_length, must_include, must_exclude)",
			},
		},
		"required": []string{"field", "current_value"},
	}
}

type OptimizeFieldInput struct {
	Field        string `json:"field"`
	CurrentValue string `json:"current_value"`
	Context      struct {
		Brand        string            `json:"brand,omitempty"`
		Category     string            `json:"category,omitempty"`
		Attributes   map[string]string `json:"attributes,omitempty"`
		GatheredFacts []struct {
			Fact   string `json:"fact"`
			Source string `json:"source"`
		} `json:"gathered_facts,omitempty"`
	} `json:"context"`
	Constraints struct {
		MaxLength   int      `json:"max_length,omitempty"`
		MustInclude []string `json:"must_include,omitempty"`
		MustExclude []string `json:"must_exclude,omitempty"`
		Tone        string   `json:"tone,omitempty"` // factual, marketing
	} `json:"constraints"`
}

type OptimizeFieldOutput struct {
	ProposedValue string `json:"proposed_value"`
	ChangesMade   []string `json:"changes_made"`
	FactsUsed     []struct {
		Fact   string `json:"fact"`
		Source string `json:"source"`
	} `json:"facts_used"`
	Confidence float64 `json:"confidence"`
}

func (t *OptimizeFieldTool) Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error) {
	var params OptimizeFieldInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	// Build the prompt
	contextJSON, _ := json.MarshalIndent(params.Context, "", "  ")
	constraintsJSON, _ := json.MarshalIndent(params.Constraints, "", "  ")

	var fieldSpecificRules string
	if params.Field == "title" {
		fieldSpecificRules = `
TEMPLATES DE TITRES PAR CATÉGORIE (GMC Best Practices):
- Apparel/Fashion: {brand} + {gender} + {type} + {color} + {size} + {material}
  Exemple: "Nike Men's Air Max 90 Black Size 42 Leather"
- Electronics: {brand} + {line} + {model} + {key_spec} + {capacity}
  Exemple: "Samsung Galaxy S24 Ultra 5G 256GB Titanium"
- Home & Garden: {brand} + {type} + {material} + {dimensions} + {style}
  Exemple: "IKEA KALLAX Shelf Wood White 77x147cm Modern"
- Beauty: {brand} + {line} + {type} + {variant} + {size}
  Exemple: "L'Oréal Revitalift Night Cream Anti-Wrinkle 50ml"

RÈGLES TITRE:
✅ Front-load keywords (70 premiers caractères visibles dans Google Shopping)
✅ Inclure attributs différenciants (couleur, taille, matériau)
✅ Max 150 caractères, optimal 70-100 caractères
❌ PAS de MAJUSCULES ABUSIVES
❌ PAS de texte promo: "SOLDES", "PROMO", "-50%", "LIVRAISON GRATUITE"
❌ PAS de keyword stuffing (répétition)
❌ PAS de symboles: ★ ♥ → ● etc.`
	} else if params.Field == "description" {
		fieldSpecificRules = `
STRUCTURE DESCRIPTION OPTIMALE:
1. Accroche - Bénéfice principal (1-2 phrases)
2. Features - Caractéristiques clés (bullet points mentaux)
3. Specs - Dimensions, matériaux, compatibilité
4. Use cases - Contextes d'utilisation, occasions

RÈGLES DESCRIPTION:
✅ Contenu unique (pas de duplicate)
✅ Keywords naturellement intégrés
✅ Informations utiles pour l'acheteur
✅ Minimum 500 caractères recommandé
❌ PAS de HTML tags
❌ PAS d'infos prix/promo/shipping
❌ PAS de liens ou références à d'autres sites`
	}

	prompt := fmt.Sprintf(`Optimise ce champ produit pour Google Merchant Center en respectant STRICTEMENT les règles suivantes:

RÈGLES CRITIQUES "NO INVENTION":
1. N'ajoute AUCUNE information qui n'est pas dans le contexte ou les gathered_facts
2. Chaque fait ajouté doit être traçable à une source
3. Pas de superlatifs non prouvés ("meilleur", "unique", "premium" sans preuve)
4. Pas d'invention de caractéristiques
%s

Champ: %s
Valeur actuelle: %s

Contexte:
%s

Contraintes:
%s

Retourne un JSON avec:
{
  "proposed_value": "...",
  "changes_made": ["description de chaque changement"],
  "facts_used": [{"fact": "...", "source": "..."}],
  "confidence": 0.X
}

Retourne UNIQUEMENT le JSON.`, fieldSpecificRules, params.Field, params.CurrentValue, string(contextJSON), string(constraintsJSON))

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
		return nil, fmt.Errorf("openai optimize: %w", err)
	}

	var result OptimizeFieldOutput
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return json.RawMessage(resp.Choices[0].Message.Content), nil
	}

	return result, nil
}

// AddAttributeTool adds a missing attribute with mandatory source
type AddAttributeTool struct{}

func (t *AddAttributeTool) Name() string { return "add_attribute" }

func (t *AddAttributeTool) Description() string {
	return "Add a missing attribute to the product. Source is MANDATORY."
}

func (t *AddAttributeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"attribute": map[string]any{
				"type":        "string",
				"description": "Attribute name (e.g., 'color', 'material', 'gender')",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "Attribute value",
			},
			"source": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type": map[string]any{
						"type": "string",
						"enum": []string{"feed", "web", "vision"},
					},
					"reference": map[string]any{
						"type":        "string",
						"description": "URL or field name",
					},
					"evidence": map[string]any{
						"type":        "string",
						"description": "Snippet or observation proving the value",
					},
				},
				"required": []string{"type", "reference", "evidence"},
			},
		},
		"required": []string{"attribute", "value", "source"},
	}
}

type AddAttributeInput struct {
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
	Source    Source `json:"source"`
}

type AddAttributeOutput struct {
	Success        bool   `json:"success"`
	Attribute      string `json:"attribute"`
	Value          string `json:"value"`
	SourceRecorded bool   `json:"source_recorded"`
}

func (t *AddAttributeTool) Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error) {
	var params AddAttributeInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	// Record the source
	session.AddSource(params.Source)

	// Add as a proposal
	session.AddProposal(
		params.Attribute,
		"", // before (empty = new attribute)
		params.Value,
		[]Source{params.Source},
		0.9, // default confidence for sourced attributes
		"low",
	)

	return AddAttributeOutput{
		Success:        true,
		Attribute:      params.Attribute,
		Value:          params.Value,
		SourceRecorded: true,
	}, nil
}
