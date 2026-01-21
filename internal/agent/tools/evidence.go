package tools

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
)

// EvidenceRegistry tracks ALL facts and their sources
// Every fact must point to: image, source URL, or original feed
// This is critical for auditability and trust
type EvidenceRegistry struct {
	mu       sync.RWMutex
	evidence map[uuid.UUID]*Evidence
	byField  map[string][]uuid.UUID // field -> evidence IDs
}

type Evidence struct {
	ID         uuid.UUID `json:"id"`
	ProductID  uuid.UUID `json:"product_id"`
	Field      string    `json:"field"`
	Value      string    `json:"value"`
	SourceType string    `json:"source_type"` // feed, image, web, user
	Source     EvidenceSource    `json:"source"`
	Confidence float64   `json:"confidence"`
	Verified   bool      `json:"verified"`
	VerifiedBy string    `json:"verified_by,omitempty"` // agent or user ID
	CreatedAt  time.Time `json:"created_at"`
}

type EvidenceSource struct {
	Type       string `json:"type"`       // feed_field, image_observation, web_page, user_input
	Reference  string `json:"reference"`  // field name, URL, or description
	Snippet    string `json:"snippet"`    // exact text/observation that supports the value
	URL        string `json:"url,omitempty"`
	ImageURL   string `json:"image_url,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

func NewEvidenceRegistry() *EvidenceRegistry {
	return &EvidenceRegistry{
		evidence: make(map[uuid.UUID]*Evidence),
		byField:  make(map[string][]uuid.UUID),
	}
}

// RegisterFromFeed creates evidence from original feed data
func (r *EvidenceRegistry) RegisterFromFeed(productID uuid.UUID, field, value string) *Evidence {
	r.mu.Lock()
	defer r.mu.Unlock()

	ev := &Evidence{
		ID:         uuid.New(),
		ProductID:  productID,
		Field:      field,
		Value:      value,
		SourceType: "feed",
		Source: EvidenceSource{
			Type:      "feed_field",
			Reference: field,
			Snippet:   value,
			Timestamp: time.Now(),
		},
		Confidence: 1.0, // Feed data is ground truth
		Verified:   true,
		VerifiedBy: "system",
		CreatedAt:  time.Now(),
	}

	r.evidence[ev.ID] = ev
	r.byField[field] = append(r.byField[field], ev.ID)

	return ev
}

// RegisterFromImage creates evidence from image analysis
func (r *EvidenceRegistry) RegisterFromImage(productID uuid.UUID, field, value, imageURL, reasoning string, confidence float64) *Evidence {
	r.mu.Lock()
	defer r.mu.Unlock()

	ev := &Evidence{
		ID:         uuid.New(),
		ProductID:  productID,
		Field:      field,
		Value:      value,
		SourceType: "image",
		Source: EvidenceSource{
			Type:      "image_observation",
			Reference: "visual_analysis",
			Snippet:   reasoning,
			ImageURL:  imageURL,
			Timestamp: time.Now(),
		},
		Confidence: confidence,
		Verified:   confidence >= 0.85, // Auto-verify high confidence
		CreatedAt:  time.Now(),
	}

	r.evidence[ev.ID] = ev
	r.byField[field] = append(r.byField[field], ev.ID)

	return ev
}

// RegisterFromWeb creates evidence from web retrieval
func (r *EvidenceRegistry) RegisterFromWeb(productID uuid.UUID, field, value, url, snippet string, confidence float64) *Evidence {
	r.mu.Lock()
	defer r.mu.Unlock()

	ev := &Evidence{
		ID:         uuid.New(),
		ProductID:  productID,
		Field:      field,
		Value:      value,
		SourceType: "web",
		Source: EvidenceSource{
			Type:      "web_page",
			Reference: url,
			Snippet:   snippet,
			URL:       url,
			Timestamp: time.Now(),
		},
		Confidence: confidence,
		Verified:   false, // Web sources need verification
		CreatedAt:  time.Now(),
	}

	r.evidence[ev.ID] = ev
	r.byField[field] = append(r.byField[field], ev.ID)

	return ev
}

// RegisterFromUser creates evidence from user input
func (r *EvidenceRegistry) RegisterFromUser(productID uuid.UUID, field, value, userID string) *Evidence {
	r.mu.Lock()
	defer r.mu.Unlock()

	ev := &Evidence{
		ID:         uuid.New(),
		ProductID:  productID,
		Field:      field,
		Value:      value,
		SourceType: "user",
		Source: EvidenceSource{
			Type:      "user_input",
			Reference: userID,
			Snippet:   "User provided value",
			Timestamp: time.Now(),
		},
		Confidence: 1.0, // User input is trusted
		Verified:   true,
		VerifiedBy: userID,
		CreatedAt:  time.Now(),
	}

	r.evidence[ev.ID] = ev
	r.byField[field] = append(r.byField[field], ev.ID)

	return ev
}

// GetEvidence retrieves evidence by ID
func (r *EvidenceRegistry) GetEvidence(id uuid.UUID) *Evidence {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.evidence[id]
}

// GetEvidenceForField returns all evidence for a specific field
func (r *EvidenceRegistry) GetEvidenceForField(field string) []*Evidence {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.byField[field]
	result := make([]*Evidence, 0, len(ids))
	for _, id := range ids {
		if ev := r.evidence[id]; ev != nil {
			result = append(result, ev)
		}
	}
	return result
}

// GetBestEvidence returns the highest confidence verified evidence for a field
func (r *EvidenceRegistry) GetBestEvidence(field string) *Evidence {
	evidence := r.GetEvidenceForField(field)
	
	var best *Evidence
	for _, ev := range evidence {
		if !ev.Verified {
			continue
		}
		if best == nil || ev.Confidence > best.Confidence {
			best = ev
		}
	}
	return best
}

// GetAllowedFacts returns a map of field -> value for all verified evidence
func (r *EvidenceRegistry) GetAllowedFacts() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	facts := make(map[string]string)
	for field := range r.byField {
		if best := r.GetBestEvidence(field); best != nil {
			facts[field] = best.Value
		}
	}
	return facts
}

// VerifyEvidence marks evidence as verified
func (r *EvidenceRegistry) VerifyEvidence(id uuid.UUID, verifiedBy string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if ev := r.evidence[id]; ev != nil {
		ev.Verified = true
		ev.VerifiedBy = verifiedBy
	}
}

// ToJSON exports the registry as JSON for auditing
func (r *EvidenceRegistry) ToJSON() (json.RawMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	evidence := make([]*Evidence, 0, len(r.evidence))
	for _, ev := range r.evidence {
		evidence = append(evidence, ev)
	}

	return json.Marshal(evidence)
}

// LoadFromFeedData populates registry from product feed data
func (r *EvidenceRegistry) LoadFromFeedData(productID uuid.UUID, data json.RawMessage) error {
	var fields map[string]interface{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	for field, value := range fields {
		if value == nil {
			continue
		}
		strValue := ""
		switch v := value.(type) {
		case string:
			strValue = v
		case float64:
			strValue = json.Number(string(rune(int(v)))).String()
		default:
			b, _ := json.Marshal(v)
			strValue = string(b)
		}
		if strValue != "" {
			r.RegisterFromFeed(productID, field, strValue)
		}
	}

	return nil
}
