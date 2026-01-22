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

// Prompt represents an editable agent prompt
type Prompt struct {
	ID          string    `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	Content     string    `json:"content" db:"content"`
	Category    string    `json:"category" db:"category"` // agent, tool
	IsDefault   bool      `json:"is_default" db:"is_default"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// TokenUsage tracks API token consumption and costs
type TokenUsage struct {
	ID               uuid.UUID `json:"id" db:"id"`
	Date             string    `json:"date" db:"date"`
	Model            string    `json:"model" db:"model"`
	PromptTokens     int       `json:"prompt_tokens" db:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens" db:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens" db:"total_tokens"`
	CostUSD          float64   `json:"cost_usd" db:"cost_usd"`
	APICalls         int       `json:"api_calls" db:"api_calls"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}

// TokenUsageStats aggregated statistics
type TokenUsageStats struct {
	TotalPromptTokens     int     `json:"total_prompt_tokens"`
	TotalCompletionTokens int     `json:"total_completion_tokens"`
	TotalTokens           int     `json:"total_tokens"`
	TotalCostUSD          float64 `json:"total_cost_usd"`
	TotalAPICalls         int     `json:"total_api_calls"`
	ByModel               []TokenUsage `json:"by_model,omitempty"`
	ByDay                 []TokenUsage `json:"by_day,omitempty"`
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

// ===== DATA FEEDS MODELS =====

// DatasetVersion represents an import version of a dataset
type DatasetVersion struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	DatasetID     uuid.UUID  `json:"dataset_id" db:"dataset_id"`
	VersionNumber int        `json:"version_number" db:"version_number"`
	FileName      string     `json:"file_name" db:"file_name"`
	RowCount      int        `json:"row_count" db:"row_count"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	CreatedBy     string     `json:"created_by" db:"created_by"`
	Notes         string     `json:"notes" db:"notes"`
}

// DatasetSnapshot represents a point-in-time snapshot of a dataset
type DatasetSnapshot struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	DatasetID    uuid.UUID  `json:"dataset_id" db:"dataset_id"`
	Name         string     `json:"name" db:"name"`
	SnapshotType string     `json:"snapshot_type" db:"snapshot_type"` // pre_enrichment, post_enrichment, manual
	ProductCount int        `json:"product_count" db:"product_count"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	CreatedBy    string     `json:"created_by" db:"created_by"`
}

// SnapshotProduct stores product data for a snapshot
type SnapshotProduct struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	SnapshotID  uuid.UUID       `json:"snapshot_id" db:"snapshot_id"`
	ProductID   uuid.UUID       `json:"product_id" db:"product_id"`
	RawData     json.RawMessage `json:"raw_data" db:"raw_data"`
	CurrentData json.RawMessage `json:"current_data" db:"current_data"`
}

// ChangeLogEntry represents an audit trail entry
type ChangeLogEntry struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	DatasetID *uuid.UUID `json:"dataset_id" db:"dataset_id"`
	ProductID *uuid.UUID `json:"product_id" db:"product_id"`
	Action    string     `json:"action" db:"action"` // import, proposal_accepted, proposal_rejected, manual_edit, export, restore
	Field     string     `json:"field" db:"field"`
	OldValue  string     `json:"old_value" db:"old_value"`
	NewValue  string     `json:"new_value" db:"new_value"`
	Source    string     `json:"source" db:"source"` // user, agent, rule
	Module    string     `json:"module" db:"module"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	CreatedBy string     `json:"created_by" db:"created_by"`
}

// ApprovalRule defines auto-approval/rejection criteria
type ApprovalRule struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	DatasetID     *uuid.UUID `json:"dataset_id" db:"dataset_id"` // nil = global
	Name          string     `json:"name" db:"name"`
	Field         string     `json:"field" db:"field"`   // empty = all fields
	Module        string     `json:"module" db:"module"` // empty = all modules
	MinConfidence float64    `json:"min_confidence" db:"min_confidence"`
	MaxRisk       string     `json:"max_risk" db:"max_risk"` // low, medium, high
	Action        string     `json:"action" db:"action"`     // auto_approve, auto_reject, flag
	Priority      int        `json:"priority" db:"priority"`
	Active        bool       `json:"active" db:"active"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     *time.Time `json:"updated_at" db:"updated_at"`
}

// JobLog represents a single log entry for a job
type JobLog struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // info, success, warning, error
	Message   string    `json:"message"`
	Details   string    `json:"details,omitempty"`
}

// JobWithDetails extends Job with execution tracking fields
type JobWithDetails struct {
	Job
	Module             string    `json:"module" db:"module"`
	TotalItems         int       `json:"total_items" db:"total_items"`
	ProcessedItems     int       `json:"processed_items" db:"processed_items"`
	ProposalsGenerated int       `json:"proposals_generated" db:"proposals_generated"`
	Logs               []JobLog  `json:"logs"`
	UpdatedAt          *time.Time `json:"updated_at" db:"updated_at"`
}

// ProposalWithProduct extends Proposal with product context
type ProposalWithProduct struct {
	Proposal
	Module            string `json:"module" db:"module"`
	ProductExternalID string `json:"product_external_id" db:"product_external_id"`
	ProductTitle      string `json:"product_title" db:"product_title"`
	DatasetID         uuid.UUID `json:"dataset_id" db:"dataset_id"`
	DatasetName       string `json:"dataset_name" db:"dataset_name"`
}

// ProposalsByModule groups proposals by optimization module
type ProposalsByModule struct {
	Module       string `json:"module"`
	Total        int    `json:"total"`
	Pending      int    `json:"pending"`
	Approved     int    `json:"approved"`
	Rejected     int    `json:"rejected"`
	AutoApproved int    `json:"auto_approved"`
}
