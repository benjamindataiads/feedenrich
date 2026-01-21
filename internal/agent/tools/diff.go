package tools

import (
	"strings"
)

// DiffEngine shows exactly what changed between before and after
// Critical for auditability and reversibility
type DiffEngine struct{}

func NewDiffEngine() *DiffEngine {
	return &DiffEngine{}
}

// Diff represents a change between two values
type Diff struct {
	Field       string       `json:"field"`
	Before      string       `json:"before"`
	After       string       `json:"after"`
	ChangeType  string       `json:"change_type"` // added, removed, modified, unchanged
	Changes     []DiffChange `json:"changes"`     // detailed changes
	AddedWords  []string     `json:"added_words"`
	RemovedWords []string    `json:"removed_words"`
	Similarity  float64      `json:"similarity"` // 0-1, how similar before/after are
}

type DiffChange struct {
	Type     string `json:"type"`     // insert, delete, equal
	Text     string `json:"text"`
	Position int    `json:"position"`
}

// ComputeDiff analyzes the difference between two values
func (d *DiffEngine) ComputeDiff(field, before, after string) *Diff {
	diff := &Diff{
		Field:       field,
		Before:      before,
		After:       after,
		Changes:     []DiffChange{},
		AddedWords:  []string{},
		RemovedWords: []string{},
	}

	// Determine change type
	if before == "" && after != "" {
		diff.ChangeType = "added"
	} else if before != "" && after == "" {
		diff.ChangeType = "removed"
	} else if before == after {
		diff.ChangeType = "unchanged"
		diff.Similarity = 1.0
		return diff
	} else {
		diff.ChangeType = "modified"
	}

	// Word-level diff
	beforeWords := tokenize(before)
	afterWords := tokenize(after)

	beforeSet := make(map[string]bool)
	afterSet := make(map[string]bool)

	for _, w := range beforeWords {
		beforeSet[strings.ToLower(w)] = true
	}
	for _, w := range afterWords {
		afterSet[strings.ToLower(w)] = true
	}

	// Find added words
	for _, w := range afterWords {
		if !beforeSet[strings.ToLower(w)] {
			diff.AddedWords = append(diff.AddedWords, w)
		}
	}

	// Find removed words
	for _, w := range beforeWords {
		if !afterSet[strings.ToLower(w)] {
			diff.RemovedWords = append(diff.RemovedWords, w)
		}
	}

	// Calculate similarity (Jaccard similarity)
	if len(beforeSet) == 0 && len(afterSet) == 0 {
		diff.Similarity = 1.0
	} else {
		intersection := 0
		for w := range beforeSet {
			if afterSet[w] {
				intersection++
			}
		}
		union := len(beforeSet) + len(afterSet) - intersection
		if union > 0 {
			diff.Similarity = float64(intersection) / float64(union)
		}
	}

	// Build detailed changes
	diff.Changes = d.buildChanges(before, after)

	return diff
}

// ComputeMultipleDiffs compares all fields in two data maps
func (d *DiffEngine) ComputeMultipleDiffs(before, after map[string]string) []*Diff {
	diffs := []*Diff{}

	// All fields from both maps
	allFields := make(map[string]bool)
	for k := range before {
		allFields[k] = true
	}
	for k := range after {
		allFields[k] = true
	}

	for field := range allFields {
		beforeVal := before[field]
		afterVal := after[field]

		if beforeVal != afterVal {
			diffs = append(diffs, d.ComputeDiff(field, beforeVal, afterVal))
		}
	}

	return diffs
}

func (d *DiffEngine) buildChanges(before, after string) []DiffChange {
	changes := []DiffChange{}

	// Simple approach: find common prefix and suffix
	i := 0
	j := 0

	// Common prefix
	for i < len(before) && i < len(after) && before[i] == after[i] {
		i++
	}

	// Common suffix
	beforeEnd := len(before) - 1
	afterEnd := len(after) - 1
	for beforeEnd > i && afterEnd > i && before[beforeEnd] == after[afterEnd] {
		beforeEnd--
		afterEnd--
		j++
	}

	// Equal prefix
	if i > 0 {
		changes = append(changes, DiffChange{
			Type:     "equal",
			Text:     before[:i],
			Position: 0,
		})
	}

	// Deleted part
	if beforeEnd >= i {
		changes = append(changes, DiffChange{
			Type:     "delete",
			Text:     before[i : beforeEnd+1],
			Position: i,
		})
	}

	// Inserted part
	if afterEnd >= i {
		changes = append(changes, DiffChange{
			Type:     "insert",
			Text:     after[i : afterEnd+1],
			Position: i,
		})
	}

	// Equal suffix
	if j > 0 {
		changes = append(changes, DiffChange{
			Type:     "equal",
			Text:     before[len(before)-j:],
			Position: len(after) - j,
		})
	}

	return changes
}

func tokenize(s string) []string {
	// Simple word tokenization
	words := []string{}
	word := strings.Builder{}

	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == ',' || r == '.' || r == ';' {
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
		} else {
			word.WriteRune(r)
		}
	}

	if word.Len() > 0 {
		words = append(words, word.String())
	}

	return words
}
