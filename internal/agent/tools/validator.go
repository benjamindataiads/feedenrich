package tools

import (
	"encoding/json"
	"regexp"
	"strings"
)

// HardRuleValidator is a DETERMINISTIC, REPRODUCIBLE, EXPLAINABLE validator
// No AI involved - pure rule-based validation
type HardRuleValidator struct {
	rules []ValidationRule
}

type ValidationRule struct {
	ID        string      `json:"id"`
	Field     string      `json:"field"`
	Type      string      `json:"type"` // required, min_length, max_length, pattern, forbidden_words, format
	Value     interface{} `json:"value"`
	Message   string      `json:"message"`
	Severity  string      `json:"severity"` // error, warning
}

type ValidationResult struct {
	Valid      bool              `json:"valid"`
	Violations []RuleViolation   `json:"violations"`
	Warnings   []RuleViolation   `json:"warnings"`
	Checked    int               `json:"rules_checked"`
}

type RuleViolation struct {
	RuleID   string `json:"rule_id"`
	Field    string `json:"field"`
	Message  string `json:"message"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

func NewHardRuleValidator() *HardRuleValidator {
	return &HardRuleValidator{
		rules: defaultGMCRules(),
	}
}

// LoadRules adds custom rules to the validator
func (v *HardRuleValidator) LoadRules(rules []ValidationRule) {
	v.rules = append(v.rules, rules...)
}

// Validate checks product data against all rules
func (v *HardRuleValidator) Validate(productData json.RawMessage) *ValidationResult {
	result := &ValidationResult{
		Valid:      true,
		Violations: []RuleViolation{},
		Warnings:   []RuleViolation{},
		Checked:    0,
	}

	// Parse product data
	var data map[string]interface{}
	if err := json.Unmarshal(productData, &data); err != nil {
		result.Valid = false
		result.Violations = append(result.Violations, RuleViolation{
			RuleID:  "parse_error",
			Field:   "_json",
			Message: "Failed to parse product data",
		})
		return result
	}

	// Check each rule
	for _, rule := range v.rules {
		result.Checked++

		fieldValue := getFieldValue(data, rule.Field)
		violation := v.checkRule(rule, fieldValue)

		if violation != nil {
			if rule.Severity == "error" {
				result.Valid = false
				result.Violations = append(result.Violations, *violation)
			} else {
				result.Warnings = append(result.Warnings, *violation)
			}
		}
	}

	return result
}

func (v *HardRuleValidator) checkRule(rule ValidationRule, value string) *RuleViolation {
	switch rule.Type {
	case "required":
		if strings.TrimSpace(value) == "" {
			return &RuleViolation{
				RuleID:   rule.ID,
				Field:    rule.Field,
				Message:  rule.Message,
				Expected: "non-empty value",
				Actual:   "(empty)",
			}
		}

	case "min_length":
		minLen, ok := rule.Value.(float64)
		if !ok {
			return nil
		}
		if len(value) < int(minLen) {
			return &RuleViolation{
				RuleID:   rule.ID,
				Field:    rule.Field,
				Message:  rule.Message,
				Expected: string(rune(int(minLen))) + "+ characters",
				Actual:   string(rune(len(value))) + " characters",
			}
		}

	case "max_length":
		maxLen, ok := rule.Value.(float64)
		if !ok {
			return nil
		}
		if len(value) > int(maxLen) {
			return &RuleViolation{
				RuleID:   rule.ID,
				Field:    rule.Field,
				Message:  rule.Message,
				Expected: "max " + string(rune(int(maxLen))) + " characters",
				Actual:   string(rune(len(value))) + " characters",
			}
		}

	case "pattern":
		pattern, ok := rule.Value.(string)
		if !ok {
			return nil
		}
		matched, _ := regexp.MatchString(pattern, value)
		if !matched {
			return &RuleViolation{
				RuleID:   rule.ID,
				Field:    rule.Field,
				Message:  rule.Message,
				Expected: "match pattern: " + pattern,
				Actual:   value,
			}
		}

	case "forbidden_words":
		words, ok := rule.Value.([]interface{})
		if !ok {
			return nil
		}
		lowerValue := strings.ToLower(value)
		for _, w := range words {
			word, ok := w.(string)
			if !ok {
				continue
			}
			if strings.Contains(lowerValue, strings.ToLower(word)) {
				return &RuleViolation{
					RuleID:   rule.ID,
					Field:    rule.Field,
					Message:  rule.Message,
					Expected: "no forbidden words",
					Actual:   "contains '" + word + "'",
				}
			}
		}

	case "url":
		if value != "" && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return &RuleViolation{
				RuleID:   rule.ID,
				Field:    rule.Field,
				Message:  rule.Message,
				Expected: "valid URL starting with http:// or https://",
				Actual:   value,
			}
		}
	}

	return nil
}

func getFieldValue(data map[string]interface{}, field string) string {
	// Try exact match first
	if val, ok := data[field]; ok {
		return toString(val)
	}

	// Try case-insensitive match
	lowerField := strings.ToLower(field)
	for k, v := range data {
		if strings.ToLower(k) == lowerField {
			return toString(v)
		}
	}

	return ""
}

func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strings.TrimRight(strings.TrimRight(strings.Replace(string(rune(int(val))), ".", "", 1), "0"), ".")
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// defaultGMCRules returns standard Google Merchant Center rules (2025)
func defaultGMCRules() []ValidationRule {
	return []ValidationRule{
		// === REQUIRED FIELDS ===
		{ID: "gmc_id_required", Field: "id", Type: "required", Message: "Product ID is required", Severity: "error"},
		{ID: "gmc_title_required", Field: "title", Type: "required", Message: "Title is required", Severity: "error"},
		{ID: "gmc_description_required", Field: "description", Type: "required", Message: "Description is required", Severity: "error"},
		{ID: "gmc_link_required", Field: "link", Type: "required", Message: "Product link is required", Severity: "error"},
		{ID: "gmc_image_required", Field: "image_link", Type: "required", Message: "Image link is required", Severity: "error"},
		{ID: "gmc_price_required", Field: "price", Type: "required", Message: "Price is required", Severity: "error"},
		{ID: "gmc_availability_required", Field: "availability", Type: "required", Message: "Availability is required", Severity: "error"},

		// === LENGTH CONSTRAINTS ===
		{ID: "gmc_title_min", Field: "title", Type: "min_length", Value: 30.0, Message: "Title should be at least 30 characters", Severity: "warning"},
		{ID: "gmc_title_max", Field: "title", Type: "max_length", Value: 150.0, Message: "Title must not exceed 150 characters", Severity: "error"},
		{ID: "gmc_description_min", Field: "description", Type: "min_length", Value: 50.0, Message: "Description should be at least 50 characters", Severity: "warning"},
		{ID: "gmc_description_max", Field: "description", Type: "max_length", Value: 5000.0, Message: "Description must not exceed 5000 characters", Severity: "error"},

		// === URL VALIDATION ===
		{ID: "gmc_link_url", Field: "link", Type: "url", Message: "Product link must be a valid URL", Severity: "error"},
		{ID: "gmc_image_url", Field: "image_link", Type: "url", Message: "Image link must be a valid URL", Severity: "error"},

		// === FORBIDDEN CONTENT ===
		{ID: "gmc_title_promo", Field: "title", Type: "forbidden_words", Value: []interface{}{"free shipping", "sale", "discount", "promo", "soldes", "-50%", "-30%", "-20%", "livraison gratuite", "gratuit", "offre", "promotion"}, Message: "Title must not contain promotional text", Severity: "error"},
		{ID: "gmc_description_promo", Field: "description", Type: "forbidden_words", Value: []interface{}{"free shipping", "livraison gratuite", "click here", "buy now", "limited time"}, Message: "Description should not contain promotional calls to action", Severity: "warning"},

		// === STRONGLY RECOMMENDED ===
		{ID: "gmc_brand_recommended", Field: "brand", Type: "required", Message: "Brand is strongly recommended for most categories", Severity: "warning"},
		{ID: "gmc_gtin_recommended", Field: "gtin", Type: "required", Message: "GTIN (EAN/UPC) is strongly recommended when available", Severity: "warning"},
		{ID: "gmc_product_type_recommended", Field: "product_type", Type: "required", Message: "Product type helps with categorization", Severity: "info"},
		{ID: "gmc_google_category_recommended", Field: "google_product_category", Type: "required", Message: "Google product category improves search relevance", Severity: "info"},

		// === APPAREL-SPECIFIC (Required in US, UK, DE, JP, FR, BR) ===
		{ID: "gmc_color_apparel", Field: "color", Type: "required", Message: "Color is required for apparel products", Severity: "warning"},
		{ID: "gmc_gender_apparel", Field: "gender", Type: "required", Message: "Gender is required for apparel (male/female/unisex)", Severity: "warning"},
		{ID: "gmc_age_group_apparel", Field: "age_group", Type: "required", Message: "Age group is required for apparel (adult/kids/infant/etc.)", Severity: "warning"},
		{ID: "gmc_size_apparel", Field: "size", Type: "required", Message: "Size is required for clothing and shoes", Severity: "warning"},

		// === VARIANT PRODUCTS ===
		{ID: "gmc_item_group_variants", Field: "item_group_id", Type: "required", Message: "Item group ID required for product variants", Severity: "info"},

		// === OPTIONAL BUT VALUABLE ===
		{ID: "gmc_material_recommended", Field: "material", Type: "required", Message: "Material improves product discoverability", Severity: "info"},
		{ID: "gmc_pattern_recommended", Field: "pattern", Type: "required", Message: "Pattern helps distinguish variants", Severity: "info"},
		{ID: "gmc_condition_recommended", Field: "condition", Type: "required", Message: "Condition (new/used/refurbished) should be specified", Severity: "info"},
	}
}
