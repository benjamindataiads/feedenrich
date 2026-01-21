package agent

import (
	"encoding/json"
	"time"

	"github.com/benjamincozon/feedenrich/internal/agent/tools"
	"github.com/benjamincozon/feedenrich/internal/models"
	"github.com/google/uuid"
)

// Implement SessionContext interface for tools

func (s *Session) GetProductData() json.RawMessage {
	if s.Product != nil {
		return s.Product.RawData
	}
	return nil
}

func (s *Session) AddProposal(field, before, after string, sources []tools.Source, confidence float64, risk string) {
	sourcesJSON, _ := json.Marshal(sources)
	
	var beforePtr *string
	if before != "" {
		beforePtr = &before
	}

	proposal := models.Proposal{
		ID:          uuid.New(),
		ProductID:   s.ProductID,
		SessionID:   &s.ID,
		Field:       field,
		BeforeValue: beforePtr,
		AfterValue:  after,
		Sources:     sourcesJSON,
		Confidence:  confidence,
		RiskLevel:   risk,
		Status:      "proposed",
		CreatedAt:   time.Now(),
	}

	s.Proposals = append(s.Proposals, proposal)
}

func (s *Session) AddSource(source tools.Source) {
	s.Sources = append(s.Sources, models.Source{
		Type:       source.Type,
		Reference:  source.Reference,
		Evidence:   source.Evidence,
		Confidence: source.Confidence,
	})
}
