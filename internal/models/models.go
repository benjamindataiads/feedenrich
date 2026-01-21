package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Dataset represents an imported TSV/CSV file
type Dataset struct {
	ID            uuid.UUID       `json:"id" db:"id"`
	Name          string          `json:"name" db:"name"`
	SourceFileURL string          `json:"source_file_url" db:"source_file_url"`
	RowCount      int             `json:"row_count" db:"row_count"`
	Status        string          `json:"status" db:"status"` // uploaded, processing, ready, error
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at" db:"updated_at"`
}

// Product represents a single product from the dataset
type Product struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	DatasetID           uuid.UUID       `json:"dataset_id" db:"dataset_id"`
	ExternalID          string          `json:"external_id" db:"external_id"`
	RawData             json.RawMessage `json:"raw_data" db:"raw_data"`
	CurrentData         json.RawMessage `json:"current_data" db:"current_data"`
	Version             int             `json:"version" db:"version"`
	Status              string          `json:"status" db:"status"` // pending, processing, enriched, needs_review
	AgentReadinessScore *float64        `json:"agent_readiness_score" db:"agent_readiness_score"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" db:"updated_at"`
}

// AgentSession represents a single run of the agent on a product
type AgentSession struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	ProductID   uuid.UUID  `json:"product_id" db:"product_id"`
	Goal        string     `json:"goal" db:"goal"`
	Status      string     `json:"status" db:"status"` // running, completed, failed, paused
	TotalSteps  int        `json:"total_steps" db:"total_steps"`
	TokensUsed  int        `json:"tokens_used" db:"tokens_used"`
	StartedAt   time.Time  `json:"started_at" db:"started_at"`
	CompletedAt *time.Time `json:"completed_at" db:"completed_at"`
}

// AgentTrace represents a single step in the agent's reasoning
type AgentTrace struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	SessionID  uuid.UUID       `json:"session_id" db:"session_id"`
	StepNumber int             `json:"step_number" db:"step_number"`
	Thought    string          `json:"thought" db:"thought"`
	ToolName   string          `json:"tool_name" db:"tool_name"`
	ToolInput  json.RawMessage `json:"tool_input" db:"tool_input"`
	ToolOutput json.RawMessage `json:"tool_output" db:"tool_output"`
	TokensUsed int             `json:"tokens_used" db:"tokens_used"`
	DurationMs int             `json:"duration_ms" db:"duration_ms"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
}

// Proposal represents a suggested change to a product field
type Proposal struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	ProductID  uuid.UUID       `json:"product_id" db:"product_id"`
	SessionID  *uuid.UUID      `json:"session_id" db:"session_id"`
	Field      string          `json:"field" db:"field"`
	BeforeValue *string        `json:"before_value" db:"before_value"`
	AfterValue string          `json:"after_value" db:"after_value"`
	Rationale  []string        `json:"rationale" db:"rationale"`
	Sources    json.RawMessage `json:"sources" db:"sources"`
	Confidence float64         `json:"confidence" db:"confidence"`
	RiskLevel  string          `json:"risk_level" db:"risk_level"` // low, medium, high
	Status     string          `json:"status" db:"status"`         // proposed, accepted, rejected, edited
	ReviewedBy *string         `json:"reviewed_by" db:"reviewed_by"`
	ReviewedAt *time.Time      `json:"reviewed_at" db:"reviewed_at"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
}

// Source represents evidence for a proposal
type Source struct {
	Type       string  `json:"type"`       // feed, web, vision
	Reference  string  `json:"reference"`  // URL or field name
	Evidence   string  `json:"evidence"`   // snippet or observation
	Confidence float64 `json:"confidence"`
}

// Rule represents a validation rule
type Rule struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	DatasetID *uuid.UUID      `json:"dataset_id" db:"dataset_id"` // nil = global
	Name      string          `json:"name" db:"name"`
	Type      string          `json:"type" db:"type"` // hard, smart
	Field     string          `json:"field" db:"field"`
	Condition json.RawMessage `json:"condition" db:"condition"`
	Message   string          `json:"message" db:"message"`
	Severity  string          `json:"severity" db:"severity"` // error, warning
	Active    bool            `json:"active" db:"active"`
	CreatedBy string          `json:"created_by" db:"created_by"` // system, agent, user
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
}

// Job represents an async processing job
type Job struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	DatasetID   uuid.UUID       `json:"dataset_id" db:"dataset_id"`
	Type        string          `json:"type" db:"type"` // enrich_all, enrich_batch, single_product
	Status      string          `json:"status" db:"status"` // pending, running, completed, failed
	Progress    json.RawMessage `json:"progress" db:"progress"`
	Config      json.RawMessage `json:"config" db:"config"`
	Error       *string         `json:"error" db:"error"`
	StartedAt   *time.Time      `json:"started_at" db:"started_at"`
	CompletedAt *time.Time      `json:"completed_at" db:"completed_at"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
}

// ProductData represents the actual product fields
type ProductData struct {
	ID          string            `json:"id,omitempty"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Link        string            `json:"link,omitempty"`
	ImageLink   string            `json:"image_link,omitempty"`
	Price       string            `json:"price,omitempty"`
	Brand       string            `json:"brand,omitempty"`
	GTIN        string            `json:"gtin,omitempty"`
	MPN         string            `json:"mpn,omitempty"`
	Condition   string            `json:"condition,omitempty"`
	Color       string            `json:"color,omitempty"`
	Size        string            `json:"size,omitempty"`
	Gender      string            `json:"gender,omitempty"`
	Material    string            `json:"material,omitempty"`
	ProductType string            `json:"product_type,omitempty"`
	Category    string            `json:"google_product_category,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"` // additional fields
}

// AnalysisResult from analyze_product tool
type AnalysisResult struct {
	CurrentData   ProductData `json:"current_data"`
	GMCCompliance struct {
		Valid  bool `json:"valid"`
		Errors []struct {
			Field    string `json:"field"`
			Issue    string `json:"issue"`
			Severity string `json:"severity"`
		} `json:"errors"`
	} `json:"gmc_compliance"`
	QualityScores struct {
		TitleQuality       float64 `json:"title_quality"`
		DescriptionQuality float64 `json:"description_quality"`
		Completeness       float64 `json:"completeness"`
		AgentReadiness     float64 `json:"agent_readiness"`
	} `json:"quality_scores"`
	MissingAttributes        []string `json:"missing_attributes"`
	ImprovementOpportunities []struct {
		Field           string `json:"field"`
		CurrentIssue    string `json:"current_issue"`
		PotentialAction string `json:"potential_action"`
	} `json:"improvement_opportunities"`
}
