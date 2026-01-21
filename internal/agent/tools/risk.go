package tools

import (
	"strings"
)

// RiskClassifier determines whether changes require human approval
// LOW / MEDIUM / HIGH → determines workflow
type RiskClassifier struct {
	// High-risk fields that always require human review
	highRiskFields map[string]bool
	// High-risk keywords that trigger review
	highRiskKeywords []string
	// Medium-risk indicators
	mediumRiskIndicators []string
}

type RiskAssessment struct {
	Level       string   `json:"level"`       // low, medium, high
	Reasons     []string `json:"reasons"`     // why this level
	RequiresHuman bool   `json:"requires_human"`
	Confidence  float64  `json:"confidence"`
}

func NewRiskClassifier() *RiskClassifier {
	return &RiskClassifier{
		highRiskFields: map[string]bool{
			"material":       true,  // Can affect allergies, compliance
			"ingredients":    true,  // Safety critical
			"weight":         true,  // Shipping, compliance
			"dimensions":     true,  // Shipping, fit
			"capacity":       true,  // Technical spec
			"voltage":        true,  // Safety
			"wattage":        true,  // Safety
			"compatibility":  true,  // Technical accuracy
			"certifications": true,  // Legal claims
			"warranty":       true,  // Legal
			"age_group":      true,  // Safety, compliance
			"energy_class":   true,  // Legal compliance
		},
		highRiskKeywords: []string{
			// Health claims
			"organic", "bio", "natural", "hypoallergenic", "dermatologically tested",
			"clinically proven", "medical", "therapeutic", "healing",
			// Safety claims
			"fireproof", "waterproof", "shockproof", "childproof", "non-toxic",
			"food-grade", "bpa-free", "lead-free",
			// Legal/certification
			"certified", "approved", "compliant", "patented", "trademarked",
			// Performance claims
			"best", "fastest", "strongest", "most efficient", "guaranteed",
			// Origin claims
			"made in", "manufactured in", "assembled in",
		},
		mediumRiskIndicators: []string{
			// Content restructuring
			"completely rewritten", "new structure",
			// Web sourced
			"from web", "external source",
			// Image inferred
			"from image", "visually identified",
			// Multiple changes
			"multiple fields", "batch update",
		},
	}
}

// AssessChange evaluates the risk of a proposed change
func (r *RiskClassifier) AssessChange(field, before, after string, sourceType string, confidence float64) *RiskAssessment {
	assessment := &RiskAssessment{
		Level:       "low",
		Reasons:     []string{},
		RequiresHuman: false,
		Confidence:  confidence,
	}

	// Check field risk
	if r.highRiskFields[strings.ToLower(field)] {
		assessment.Level = "high"
		assessment.RequiresHuman = true
		assessment.Reasons = append(assessment.Reasons, "high-risk field: "+field)
	}

	// Check for high-risk keywords in new content
	lowerAfter := strings.ToLower(after)
	for _, keyword := range r.highRiskKeywords {
		if strings.Contains(lowerAfter, strings.ToLower(keyword)) {
			// Check if keyword was already in before
			if !strings.Contains(strings.ToLower(before), strings.ToLower(keyword)) {
				assessment.Level = "high"
				assessment.RequiresHuman = true
				assessment.Reasons = append(assessment.Reasons, "new high-risk keyword: "+keyword)
			}
		}
	}

	// Check source type risk
	if sourceType == "web" && assessment.Level != "high" {
		assessment.Level = "medium"
		assessment.Reasons = append(assessment.Reasons, "sourced from web")
	}

	if sourceType == "image" && assessment.Level != "high" {
		assessment.Level = "medium"
		assessment.Reasons = append(assessment.Reasons, "inferred from image")
	}

	// Check confidence
	if confidence < 0.7 && assessment.Level != "high" {
		assessment.Level = "medium"
		assessment.Reasons = append(assessment.Reasons, "low confidence: "+string(rune(int(confidence*100)))+"%")
	}

	if confidence < 0.5 {
		assessment.Level = "high"
		assessment.RequiresHuman = true
		assessment.Reasons = append(assessment.Reasons, "very low confidence")
	}

	// Check magnitude of change
	changeRatio := calculateChangeRatio(before, after)
	if changeRatio > 0.7 && assessment.Level != "high" {
		assessment.Level = "medium"
		assessment.Reasons = append(assessment.Reasons, "significant content change")
	}
	if changeRatio > 0.9 {
		assessment.Level = "high"
		assessment.RequiresHuman = true
		assessment.Reasons = append(assessment.Reasons, "near-complete rewrite")
	}

	// Low risk indicators
	if assessment.Level == "low" && len(assessment.Reasons) == 0 {
		if sourceType == "feed" {
			assessment.Reasons = append(assessment.Reasons, "data from original feed")
		}
		if confidence >= 0.9 {
			assessment.Reasons = append(assessment.Reasons, "high confidence")
		}
		if changeRatio < 0.3 {
			assessment.Reasons = append(assessment.Reasons, "minor change")
		}
	}

	return assessment
}

// AssessBatchChanges evaluates multiple changes together
func (r *RiskClassifier) AssessBatchChanges(changes []struct {
	Field      string
	Before     string
	After      string
	SourceType string
	Confidence float64
}) *RiskAssessment {
	overall := &RiskAssessment{
		Level:       "low",
		Reasons:     []string{},
		RequiresHuman: false,
		Confidence:  1.0,
	}

	highCount := 0
	mediumCount := 0
	totalConfidence := 0.0

	for _, change := range changes {
		individual := r.AssessChange(change.Field, change.Before, change.After, change.SourceType, change.Confidence)
		totalConfidence += individual.Confidence

		switch individual.Level {
		case "high":
			highCount++
			overall.Reasons = append(overall.Reasons, individual.Reasons...)
		case "medium":
			mediumCount++
		}

		if individual.RequiresHuman {
			overall.RequiresHuman = true
		}
	}

	// Determine overall level
	if highCount > 0 {
		overall.Level = "high"
		overall.RequiresHuman = true
	} else if mediumCount >= 3 || len(changes) > 5 {
		overall.Level = "medium"
		overall.Reasons = append(overall.Reasons, "multiple medium-risk changes")
	} else if mediumCount > 0 {
		overall.Level = "medium"
	}

	if len(changes) > 0 {
		overall.Confidence = totalConfidence / float64(len(changes))
	}

	return overall
}

// ShouldRequireHumanReview returns true if human review is mandatory
func (r *RiskClassifier) ShouldRequireHumanReview(assessment *RiskAssessment) bool {
	if assessment.RequiresHuman {
		return true
	}
	if assessment.Level == "high" {
		return true
	}
	if assessment.Confidence < 0.6 {
		return true
	}
	return false
}

func calculateChangeRatio(before, after string) float64 {
	if before == "" && after == "" {
		return 0
	}
	if before == "" {
		return 1.0 // completely new
	}
	if after == "" {
		return 1.0 // completely removed
	}

	// Calculate Levenshtein-like distance ratio
	beforeWords := strings.Fields(strings.ToLower(before))
	afterWords := strings.Fields(strings.ToLower(after))

	beforeSet := make(map[string]int)
	afterSet := make(map[string]int)

	for _, w := range beforeWords {
		beforeSet[w]++
	}
	for _, w := range afterWords {
		afterSet[w]++
	}

	// Count common words
	common := 0
	for w, count := range beforeSet {
		if afterCount, ok := afterSet[w]; ok {
			if count < afterCount {
				common += count
			} else {
				common += afterCount
			}
		}
	}

	total := len(beforeWords) + len(afterWords)
	if total == 0 {
		return 0
	}

	// Ratio of changed content (1 - overlap)
	overlap := float64(common*2) / float64(total)
	return 1 - overlap
}
