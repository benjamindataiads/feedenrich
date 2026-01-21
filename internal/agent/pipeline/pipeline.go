package pipeline

import (
	"context"
	"encoding/json"
	"time"

	"github.com/benjamincozon/feedenrich/internal/agent/agents"
	"github.com/benjamincozon/feedenrich/internal/agent/tools"
	"github.com/benjamincozon/feedenrich/internal/config"
	"github.com/benjamincozon/feedenrich/internal/models"
	"github.com/google/uuid"
)

// Pipeline orchestrates the 6-agent workflow with proper separation of concerns
// Flow: Audit → Evidence → Retrieval → Plan → Execute → Control
type Pipeline struct {
	config *config.Config

	// Agents
	auditor    *agents.ProductAuditor
	evidence   *agents.ImageEvidenceAgent
	retrieval  *agents.KnowledgeRetrievalAgent
	planner    *agents.OptimizationPlanner
	writer     *agents.CopyExecutionAgent
	controller *agents.ControllerAgent

	// Tools
	validator *tools.HardRuleValidator
	differ    *tools.DiffEngine
	registry  *tools.EvidenceRegistry
	risk      *tools.RiskClassifier

	// Callbacks for real-time updates
	callbacks PipelineCallbacks
}

type PipelineCallbacks struct {
	OnStageStart  func(stage string)
	OnStageEnd    func(stage string, result interface{})
	OnProposal    func(proposal *Proposal)
	OnRejection   func(field string, reason string)
	OnHumanNeeded func(field string, reason string)
	OnComplete    func(summary *PipelineSummary)
	OnError       func(stage string, err error)
}

// PipelineResult contains the complete output with full audit trail
type PipelineResult struct {
	ProductID     uuid.UUID              `json:"product_id"`
	StartedAt     time.Time              `json:"started_at"`
	CompletedAt   time.Time              `json:"completed_at"`
	Stages        []StageResult          `json:"stages"`
	Proposals     []*Proposal            `json:"proposals"`
	Rejections    []*Rejection           `json:"rejections"`
	HumanRequired []*HumanReviewRequest  `json:"human_required"`
	EvidenceTrail json.RawMessage        `json:"evidence_trail"`
	Summary       *PipelineSummary       `json:"summary"`
}

type StageResult struct {
	Stage      string          `json:"stage"`
	StartedAt  time.Time       `json:"started_at"`
	EndedAt    time.Time       `json:"ended_at"`
	DurationMs int64           `json:"duration_ms"`
	Output     json.RawMessage `json:"output"`
	Error      string          `json:"error,omitempty"`
}

type Proposal struct {
	ID          uuid.UUID            `json:"id"`
	Field       string               `json:"field"`
	Before      string               `json:"before"`
	After       string               `json:"after"`
	Objective   string               `json:"objective"`
	FactsUsed   []agents.FactUsage   `json:"facts_used"`
	Risk        *tools.RiskAssessment `json:"risk"`
	Verified    bool                 `json:"verified"`
	Confidence  float64              `json:"confidence"`
}

type Rejection struct {
	Field    string   `json:"field"`
	Reason   string   `json:"reason"`
	Evidence string   `json:"evidence"`
	Stage    string   `json:"stage"` // which agent rejected
}

type HumanReviewRequest struct {
	Field     string `json:"field"`
	Reason    string `json:"reason"`
	RiskLevel string `json:"risk_level"`
	Context   string `json:"context"`
}

type PipelineSummary struct {
	TotalStages      int     `json:"total_stages"`
	ProposalsCreated int     `json:"proposals_created"`
	ProposalsApproved int    `json:"proposals_approved"`
	ProposalsRejected int    `json:"proposals_rejected"`
	HumanReviewNeeded int    `json:"human_review_needed"`
	DurationMs       int64   `json:"duration_ms"`
	ScoreBefore      float64 `json:"score_before"`
	ScoreAfter       float64 `json:"score_after"`
}

// NewPipeline creates a new enrichment pipeline
func NewPipeline(cfg *config.Config) *Pipeline {
	return &Pipeline{
		config:     cfg,
		auditor:    agents.NewProductAuditor(cfg),
		evidence:   agents.NewImageEvidenceAgent(cfg),
		retrieval:  agents.NewKnowledgeRetrievalAgent(cfg),
		planner:    agents.NewOptimizationPlanner(cfg),
		writer:     agents.NewCopyExecutionAgent(cfg),
		controller: agents.NewControllerAgent(cfg),
		validator:  tools.NewHardRuleValidator(),
		differ:     tools.NewDiffEngine(),
		registry:   tools.NewEvidenceRegistry(),
		risk:       tools.NewRiskClassifier(),
	}
}

// SetCallbacks sets the event callbacks for real-time updates
func (p *Pipeline) SetCallbacks(cb PipelineCallbacks) {
	p.callbacks = cb
}

// Run executes the full pipeline on a product
func (p *Pipeline) Run(ctx context.Context, product *models.Product) (*PipelineResult, error) {
	result := &PipelineResult{
		ProductID:     product.ID,
		StartedAt:     time.Now(),
		Stages:        []StageResult{},
		Proposals:     []*Proposal{},
		Rejections:    []*Rejection{},
		HumanRequired: []*HumanReviewRequest{},
	}

	// Initialize evidence registry with feed data
	p.registry = tools.NewEvidenceRegistry()
	if err := p.registry.LoadFromFeedData(product.ID, product.RawData); err != nil {
		return nil, err
	}

	// Stage 1: Hard Rule Validation (deterministic)
	stage1 := p.runStage(ctx, "validate", func() (interface{}, error) {
		return p.validator.Validate(product.RawData), nil
	})
	result.Stages = append(result.Stages, stage1)

	// Stage 2: Product Audit (AI judge)
	var auditResult *agents.AuditOutput
	stage2 := p.runStage(ctx, "audit", func() (interface{}, error) {
		input := agents.AuditInput{
			ProductData: product.RawData,
			GMCRules:    getDefaultGMCRules(),
		}
		var err error
		auditResult, err = p.auditor.Audit(ctx, input)
		return auditResult, err
	})
	result.Stages = append(result.Stages, stage2)

	if auditResult == nil {
		result.CompletedAt = time.Now()
		return result, nil
	}

	// Stage 3: Image Evidence (if image available)
	var imageEvidence *agents.ImageEvidenceOutput
	imageURL := extractImageURL(product.RawData)
	if imageURL != "" {
		stage3 := p.runStage(ctx, "image_evidence", func() (interface{}, error) {
			input := agents.ImageEvidenceInput{
				ImageURL:           imageURL,
				AttributesToVerify: []string{"color", "material", "style"},
			}
			var err error
			imageEvidence, err = p.evidence.ExtractEvidence(ctx, input)
			return imageEvidence, err
		})
		result.Stages = append(result.Stages, stage3)

		// Register image evidence
		if imageEvidence != nil {
			for _, obs := range imageEvidence.Observations {
				valStr, _ := json.Marshal(obs.Value)
				p.registry.RegisterFromImage(product.ID, obs.Attribute, string(valStr), imageURL, obs.Reasoning, obs.Confidence)
			}
		}
	}

	// Stage 4: Knowledge Retrieval (if needed)
	var retrievedFacts *agents.RetrievalOutput
	missingFields := getMissingFields(product.RawData, auditResult)
	if len(missingFields) > 0 {
		stage4 := p.runStage(ctx, "retrieval", func() (interface{}, error) {
			input := agents.RetrievalInput{
				ProductTitle: extractField(product.RawData, "title"),
				Brand:        extractField(product.RawData, "brand"),
				GTIN:         extractField(product.RawData, "gtin"),
				ProductURL:   extractField(product.RawData, "link"),
				FieldsNeeded: missingFields,
			}
			var err error
			retrievedFacts, err = p.retrieval.RetrieveFacts(ctx, input)
			return retrievedFacts, err
		})
		result.Stages = append(result.Stages, stage4)

		// Register retrieved facts
		if retrievedFacts != nil {
			for _, fact := range retrievedFacts.Facts {
				p.registry.RegisterFromWeb(product.ID, fact.Field, fact.Value, fact.URL, fact.Evidence, fact.Confidence)
			}
		}
	}

	// Stage 5: Optimization Planning
	var plan *agents.PlannerOutput
	stage5 := p.runStage(ctx, "plan", func() (interface{}, error) {
		input := agents.PlannerInput{
			ProductData: product.RawData,
			AuditResult: auditResult,
		}
		input.AvailableEvidence.ImageEvidence = imageEvidence
		input.AvailableEvidence.RetrievedFacts = retrievedFacts
		var err error
		plan, err = p.planner.Plan(ctx, input)
		return plan, err
	})
	result.Stages = append(result.Stages, stage5)

	if plan == nil {
		result.CompletedAt = time.Now()
		return result, nil
	}

	// Handle fields that require human review
	for _, hr := range plan.RequireHuman {
		req := &HumanReviewRequest{
			Field:     hr.Field,
			Reason:    hr.Reason,
			RiskLevel: hr.RiskLevel,
		}
		result.HumanRequired = append(result.HumanRequired, req)
		if p.callbacks.OnHumanNeeded != nil {
			p.callbacks.OnHumanNeeded(hr.Field, hr.Reason)
		}
	}

	// Stage 6: Execute & Control for each action
	for _, action := range plan.Actions {
		// Skip if requires human
		requiresHuman := false
		for _, hr := range plan.RequireHuman {
			if hr.Field == action.Field {
				requiresHuman = true
				break
			}
		}
		if requiresHuman {
			continue
		}

		// Build allowed facts for this action
		allowedFacts := make(map[string]string)
		for _, fieldName := range action.AllowedFacts {
			if ev := p.registry.GetBestEvidence(fieldName); ev != nil {
				allowedFacts[fieldName] = ev.Value
			}
		}

		// Add current field value
		currentValue := extractField(product.RawData, action.Field)
		if currentValue != "" {
			allowedFacts["current_"+action.Field] = currentValue
		}

		// Execute writing
		writerInput := agents.WriterInput{
			Field:          action.Field,
			CurrentValue:   currentValue,
			Objective:      action.Objective,
			AllowedFacts:   allowedFacts,
			ForbiddenFacts: action.ForbiddenFacts,
			Constraints:    action.Constraints,
		}

		writerOutput, err := p.writer.Execute(ctx, writerInput)
		if err != nil {
			result.Rejections = append(result.Rejections, &Rejection{
				Field:  action.Field,
				Reason: err.Error(),
				Stage:  "writer",
			})
			continue
		}

		// Validate with controller
		controlInput := agents.ControllerInput{
			Field:            action.Field,
			Before:           currentValue,
			After:            writerOutput.After,
			FactsUsed:        writerOutput.FactsUsed,
			AllowedFacts:     allowedFacts,
			Constraints:      action.Constraints,
			WriterConfidence: writerOutput.Confidence,
		}

		controlOutput, err := p.controller.Validate(ctx, controlInput)
		if err != nil {
			result.Rejections = append(result.Rejections, &Rejection{
				Field:  action.Field,
				Reason: err.Error(),
				Stage:  "controller",
			})
			continue
		}

		// Handle rejection
		if !controlOutput.Approved {
			for _, rej := range controlOutput.Rejections {
				rejection := &Rejection{
					Field:    action.Field,
					Reason:   rej.Reason,
					Evidence: rej.Evidence,
					Stage:    "controller",
				}
				result.Rejections = append(result.Rejections, rejection)
				if p.callbacks.OnRejection != nil {
					p.callbacks.OnRejection(action.Field, rej.Reason)
				}
			}
			continue
		}

		// Assess risk
		riskAssessment := p.risk.AssessChange(action.Field, currentValue, writerOutput.After, "mixed", writerOutput.Confidence)

		// Create proposal
		proposal := &Proposal{
			ID:         uuid.New(),
			Field:      action.Field,
			Before:     currentValue,
			After:      writerOutput.After,
			Objective:  action.Objective,
			FactsUsed:  writerOutput.FactsUsed,
			Risk:       riskAssessment,
			Verified:   controlOutput.Verification.FactsVerified,
			Confidence: controlOutput.Verification.OverallConfidence,
		}
		result.Proposals = append(result.Proposals, proposal)

		if p.callbacks.OnProposal != nil {
			p.callbacks.OnProposal(proposal)
		}

		// If high risk, also flag for human review
		if p.risk.ShouldRequireHumanReview(riskAssessment) {
			result.HumanRequired = append(result.HumanRequired, &HumanReviewRequest{
				Field:     action.Field,
				Reason:    "Risk assessment: " + riskAssessment.Level,
				RiskLevel: riskAssessment.Level,
			})
		}
	}

	// Build summary
	result.CompletedAt = time.Now()
	result.EvidenceTrail, _ = p.registry.ToJSON()
	result.Summary = &PipelineSummary{
		TotalStages:       len(result.Stages),
		ProposalsCreated:  len(result.Proposals),
		ProposalsApproved: len(result.Proposals),
		ProposalsRejected: len(result.Rejections),
		HumanReviewNeeded: len(result.HumanRequired),
		DurationMs:        result.CompletedAt.Sub(result.StartedAt).Milliseconds(),
		ScoreBefore:       auditResult.Scores.AgentReadiness,
	}

	// Calculate score after
	if auditResult != nil {
		result.Summary.ScoreAfter = auditResult.Scores.AgentReadiness + float64(len(result.Proposals))*0.05
		if result.Summary.ScoreAfter > 1.0 {
			result.Summary.ScoreAfter = 1.0
		}
	}

	if p.callbacks.OnComplete != nil {
		p.callbacks.OnComplete(result.Summary)
	}

	return result, nil
}

func (p *Pipeline) runStage(ctx context.Context, name string, fn func() (interface{}, error)) StageResult {
	start := time.Now()

	if p.callbacks.OnStageStart != nil {
		p.callbacks.OnStageStart(name)
	}

	output, err := fn()
	end := time.Now()

	result := StageResult{
		Stage:      name,
		StartedAt:  start,
		EndedAt:    end,
		DurationMs: end.Sub(start).Milliseconds(),
	}

	if err != nil {
		result.Error = err.Error()
		if p.callbacks.OnError != nil {
			p.callbacks.OnError(name, err)
		}
	} else {
		result.Output, _ = json.Marshal(output)
	}

	if p.callbacks.OnStageEnd != nil {
		p.callbacks.OnStageEnd(name, output)
	}

	return result
}

// Helper functions

func getDefaultGMCRules() []agents.GMCRule {
	return []agents.GMCRule{
		{Field: "title", Requirement: "30-150 characters, include brand and product type", Severity: "error"},
		{Field: "description", Requirement: "50+ characters, informative", Severity: "warning"},
		{Field: "image_link", Requirement: "valid URL, no watermarks", Severity: "error"},
		{Field: "price", Requirement: "required, valid format with currency", Severity: "error"},
		{Field: "brand", Requirement: "recommended for most categories", Severity: "warning"},
		{Field: "gtin", Requirement: "required if available, valid format", Severity: "warning"},
	}
}

func extractImageURL(data json.RawMessage) string {
	var fields map[string]interface{}
	json.Unmarshal(data, &fields)

	for _, key := range []string{"image_link", "image link", "imageLink", "image", "Image"} {
		if val, ok := fields[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				return str
			}
		}
	}
	return ""
}

func extractField(data json.RawMessage, field string) string {
	var fields map[string]interface{}
	json.Unmarshal(data, &fields)

	// Try exact match
	if val, ok := fields[field]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}

	// Try common variations
	variations := map[string][]string{
		"title":       {"title", "titre", "Titre", "Title"},
		"description": {"description", "Description"},
		"brand":       {"brand", "marque", "Brand", "Marque"},
		"gtin":        {"gtin", "GTIN", "ean", "EAN", "upc", "UPC"},
		"link":        {"link", "url", "URL", "Link"},
	}

	if vars, ok := variations[field]; ok {
		for _, v := range vars {
			if val, ok := fields[v]; ok {
				if str, ok := val.(string); ok {
					return str
				}
			}
		}
	}

	return ""
}

func getMissingFields(data json.RawMessage, audit *agents.AuditOutput) []string {
	if audit == nil {
		return []string{}
	}
	return audit.Missing
}
