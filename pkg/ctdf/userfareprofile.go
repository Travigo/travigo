package ctdf

import (
	"fmt"
	"strings"
	"time"
)

// UserFareProfile stores the passenger details and discount cards used by the
// journey planner when it requests a fare quote.
type UserFareProfile struct {
	UserID               string              `bson:"userid" json:"-"`
	Age                  *int                `bson:"age,omitempty" json:"age,omitempty"`
	DiscountCards        []FareCardSelection `bson:"discountcards,omitempty" json:"discount_cards"`
	ModificationDateTime time.Time           `bson:"modificationdatetime" json:"updated_at"`
}

// FareCardSelection identifies an imported fare card or rider category by its
// source identity so selections survive a new fare version.
type FareCardSelection struct {
	DatasetID        string `bson:"datasetid" json:"dataset_id"`
	SourceIdentifier string `bson:"sourceidentifier" json:"source_identifier"`
	Name             string `bson:"name,omitempty" json:"name,omitempty"`
	Kind             string `bson:"kind,omitempty" json:"kind,omitempty"`
}

// FareCardOption is the public catalogue entry exposed to account settings.
type FareCardOption struct {
	DatasetID        string    `json:"dataset_id"`
	SourceIdentifier string    `json:"source_identifier"`
	Code             string    `json:"code,omitempty"`
	Name             string    `json:"name"`
	Provider         string    `json:"provider,omitempty"`
	Kind             string    `json:"kind"`
	Type             string    `json:"type,omitempty"`
	EligibilityNotes []string  `json:"eligibility_notes,omitempty"`
	ProofRequired    string    `json:"proof_required,omitempty"`
	EligibilityURL   string    `json:"eligibility_url,omitempty"`
	EffectiveFrom    time.Time `json:"effective_from,omitempty"`
	EffectiveTo      time.Time `json:"effective_to,omitempty"`
}

func (profile UserFareProfile) Validate() error {
	if profile.Age != nil && (*profile.Age < 0 || *profile.Age > 120) {
		return fmt.Errorf("fare profile age must be between 0 and 120")
	}
	if len(profile.DiscountCards) > 30 {
		return fmt.Errorf("fare profile may contain at most 30 discount cards")
	}
	seen := make(map[string]bool, len(profile.DiscountCards))
	selectedRiderCategory := false
	for _, card := range profile.DiscountCards {
		if strings.TrimSpace(card.DatasetID) == "" || strings.TrimSpace(card.SourceIdentifier) == "" {
			return fmt.Errorf("fare profile discount cards require a dataset and source identifier")
		}
		if len(card.Name) > 200 {
			return fmt.Errorf("fare profile option name must not exceed 200 characters")
		}
		key := strings.TrimSpace(card.DatasetID) + "\x00" + strings.TrimSpace(card.SourceIdentifier)
		if seen[key] {
			return fmt.Errorf("fare profile contains a duplicate discount card")
		}
		seen[key] = true
		if card.Kind == "fare_category" {
			if selectedRiderCategory {
				return fmt.Errorf("fare profile may contain only one passenger eligibility category")
			}
			selectedRiderCategory = true
		} else if card.Kind != "" && card.Kind != "discount_card" {
			return fmt.Errorf("fare profile contains an unsupported fare option kind")
		}
	}
	return nil
}
