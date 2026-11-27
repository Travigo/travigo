package journeyplanner

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func resolveFarePassenger(catalogue *fareCatalogue, requested string, age *int, cards []ctdf.FareCardSelection, travelDate time.Time) (map[string]bool, string, bool, string) {
	refs, assumption, found := resolveFareRiderRefsForAge(catalogue, requested, age)
	profileRefs, profileAssumption, reason := selectedFareProfileRiderRefs(catalogue, cards, age, travelDate)
	if len(profileRefs) > 0 {
		return profileRefs, profileAssumption, true, reason
	}
	return refs, assumption, found, reason
}

func selectedFareProfileRiderRefs(catalogue *fareCatalogue, selections []ctdf.FareCardSelection, age *int, travelDate time.Time) (map[string]bool, string, string) {
	selected := make(map[string]bool)
	assumptions := make([]string, 0, 1)
	reason := ""
	for _, selection := range selections {
		if selection.DatasetID != catalogue.datasetID || selection.Kind != "fare_category" {
			continue
		}
		matched := false
		ageRejected, dateRejected := false, false
		chosenRef := ""
		var chosen ctdf.FareRiderCategory
		for ref, rider := range catalogue.riders {
			if rider.SourceIdentifier != selection.SourceIdentifier || fareRiderIsDiscountCard(rider) || rider.IsAgeOnlyCategory() ||
				(rider.IsDefault && !rider.RequiresEligibilityProof()) {
				continue
			}
			matched = true
			if rider.MinimumAge != nil || rider.MaximumAge != nil {
				if age == nil || !fareRiderAgeMatches(rider, *age) {
					ageRejected = true
					continue
				}
			}
			if rider.EffectiveFrom.IsZero() && rider.EffectiveTo.IsZero() {
				// No source validity range means the profile is not date-limited.
			} else if travelDate.IsZero() || !fareDateWithinRange(travelDate, rider.EffectiveFrom, rider.EffectiveTo) {
				dateRejected = true
				continue
			}
			if chosenRef == "" || rider.EffectiveFrom.After(chosen.EffectiveFrom) {
				chosenRef, chosen = ref, rider
			}
		}
		if chosenRef == "" {
			switch {
			case !matched:
				reason = appendFareReason(reason, "A selected fare eligibility profile is no longer present in the active fare catalogue.")
			case ageRejected:
				reason = appendFareReason(reason, "A selected fare eligibility profile does not match the passenger age or needs an age to be entered.")
			case dateRejected:
				reason = appendFareReason(reason, "A selected fare eligibility profile is not valid on the journey date or its validity could not be checked.")
			}
			continue
		}
		selected[chosenRef] = true
		assumptions = append(assumptions, chosen.Name)
	}
	if len(selected) == 0 {
		return selected, "", reason
	}
	sort.Strings(assumptions)
	label := "Fare eligibility: " + strings.Join(assumptions, ", ") + "."
	if age != nil {
		label = fmt.Sprintf("Passenger age: %d; %s", *age, label)
	}
	return selected, label, reason
}
