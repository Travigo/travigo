package journeyplanner

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type farePriceVariant struct {
	key           string
	price         ctdf.FareMoney
	conditions    []string
	partialReason string
}

func railFarePriceVariants(catalogue *fareCatalogue, product ctdf.FareProduct, base ctdf.FareMoney, travelDate time.Time, age *int, cards []ctdf.FareRiderCategory, nonStandardDiscount, minimumFareApplies bool) []farePriceVariant {
	if product.RailTicket == nil {
		variant := farePriceVariant{key: "base", price: base}
		for _, card := range cards {
			variant.conditions = append(variant.conditions, "Railcard requested: "+card.Name+"; this fare source does not provide a usable railcard discount rule.")
			variant.partialReason = appendFareReason(variant.partialReason, "A selected discount card is imported, but this fare source does not expose a discount rule that can be applied to the quote.")
		}
		return []farePriceVariant{variant}
	}

	statusCode, passengerLabel := "000", ""
	if age != nil {
		switch {
		case *age < 5:
			return []farePriceVariant{{
				key:           "under-five",
				price:         base,
				conditions:    []string{"Passenger age: under 5"},
				partialReason: "The rail fare data does not say whether an under-5 passenger needs a separate seat; this quote keeps the published adult price.",
			}}
		case *age <= 15:
			statusCode, passengerLabel = "001", "Child"
		default:
			passengerLabel = "Adult"
		}
	}

	baseVariant := railFarePriceVariant(catalogue, product, base, travelDate, statusCode, "base", passengerLabel)
	variants := []farePriceVariant{baseVariant}
	for _, card := range cards {
		cardVariant := farePriceVariant{
			key:        "card:" + fareCardStableIdentifier(card),
			price:      base,
			conditions: []string{"Railcard requested: " + card.Name},
		}
		if !fareRailcardSupportsSinglePassenger(card, age) {
			cardVariant.conditions = append(cardVariant.conditions, "The railcard's group or route eligibility cannot be checked for this single-passenger quote.")
			cardVariant.partialReason = "The selected railcard has group, route, train or date restrictions that this quote cannot evaluate."
			variants = append(variants, cardVariant)
			continue
		}
		if travelDate.IsZero() {
			cardVariant.conditions = append(cardVariant.conditions, "The journey date is unavailable, so railcard validity could not be checked.")
			cardVariant.partialReason = "The journey date is unavailable, so railcard validity could not be checked."
			variants = append(variants, cardVariant)
			continue
		}
		if !fareDateWithinRange(travelDate, card.EffectiveFrom, card.EffectiveTo) {
			cardVariant.conditions = append(cardVariant.conditions, "The railcard is outside its imported validity dates.")
			cardVariant.partialReason = "A selected railcard is not valid on the journey date."
			variants = append(variants, cardVariant)
			continue
		}
		cardStatus := card.AdultStatusCode
		if age != nil && *age < 16 {
			cardStatus = card.ChildStatusCode
			if cardStatus == "" {
				cardStatus = "001"
			}
		}
		if cardStatus == "" {
			cardVariant.conditions = append(cardVariant.conditions, "No status code is supplied for this railcard and passenger age.")
			cardVariant.partialReason = "The selected railcard has no imported status code for this passenger age."
			variants = append(variants, cardVariant)
			continue
		}
		cardVariant = railFarePriceVariant(catalogue, product, base, travelDate, cardStatus, cardVariant.key, passengerLabel)
		cardVariant.conditions = append(cardVariant.conditions, "Railcard requested: "+card.Name)
		if nonStandardDiscount && cardStatus != "000" {
			cardVariant.price = base
			cardVariant.partialReason = appendFareReason(cardVariant.partialReason, "This fare has non-standard discount conditions that are not imported into the railcard price calculation.")
		} else if cardStatus != "000" && minimumFareApplies && (age == nil || *age >= 16) {
			minimum, found := selectedRailcardMinimumFare(catalogue, card, product, travelDate)
			if !found {
				cardVariant.price = base
				cardVariant.partialReason = appendFareReason(cardVariant.partialReason, "A train restriction requires a railcard minimum fare, but no valid minimum-fare record was imported for this card and ticket.")
			} else if cardVariant.price.AmountMinor < minimum.AmountPence {
				cardVariant.price.AmountMinor = minimum.AmountPence
				cardVariant.conditions = append(cardVariant.conditions, fmt.Sprintf("Railcard minimum fare applied: %d pence", minimum.AmountPence))
			}
		}
		variants = append(variants, cardVariant)
	}
	if nonStandardDiscount && statusCode != "000" {
		variants[0].price = base
		variants[0].partialReason = appendFareReason(variants[0].partialReason, "This fare has non-standard discount conditions that are not imported into the passenger price calculation.")
	}
	return variants
}

func selectedRailcardMinimumFare(catalogue *fareCatalogue, card ctdf.FareRiderCategory, product ctdf.FareProduct, travelDate time.Time) (ctdf.FareRailcardMinimumFare, bool) {
	if catalogue == nil || product.RailTicket == nil || product.RailTicket.TicketCode == "" || card.RailcardCode == "" || travelDate.IsZero() {
		return ctdf.FareRailcardMinimumFare{}, false
	}
	date := time.Date(travelDate.Year(), travelDate.Month(), travelDate.Day(), 0, 0, 0, 0, time.UTC)
	var selected ctdf.FareRailcardMinimumFare
	found := false
	for _, minimum := range catalogue.railcardMinimumFares {
		if minimum.RailcardCode != card.RailcardCode || minimum.TicketCode != product.RailTicket.TicketCode ||
			!fareDateWithinRange(date, minimum.EffectiveFrom, minimum.EffectiveTo) {
			continue
		}
		if !found || minimum.EffectiveFrom.After(selected.EffectiveFrom) {
			selected, found = minimum, true
		}
	}
	return selected, found
}

func railFarePriceVariant(catalogue *fareCatalogue, product ctdf.FareProduct, base ctdf.FareMoney, travelDate time.Time, statusCode, key, passengerLabel string) farePriceVariant {
	variant := farePriceVariant{key: key, price: base}
	if passengerLabel != "" {
		variant.conditions = append(variant.conditions, "Passenger category: "+passengerLabel)
	}
	if statusCode == "000" {
		return variant
	}
	if product.RailTicket == nil || strings.TrimSpace(product.RailTicket.DiscountCategory) == "" {
		variant.partialReason = "The ticket has no imported discount category for the selected passenger status."
		return variant
	}
	if travelDate.IsZero() {
		variant.partialReason = "The journey date is unavailable, so passenger discounts could not be checked."
		return variant
	}
	if base.Currency != "GBP" || base.Exponent != 2 {
		variant.partialReason = "The rail status discount can only be calculated for a GBP fare in pence."
		return variant
	}
	statusExists := false
	var selected *ctdf.FareRailStatusDiscount
	date := time.Date(travelDate.Year(), travelDate.Month(), travelDate.Day(), 0, 0, 0, 0, time.UTC)
	category := normalizedRailDiscountCategory(product.RailTicket.DiscountCategory)
	for index := range catalogue.railStatusDiscounts {
		discount := &catalogue.railStatusDiscounts[index]
		if discount.StatusCode != statusCode || !fareDateWithinRange(date, discount.EffectiveFrom, discount.EffectiveTo) {
			continue
		}
		statusExists = true
		if normalizedRailDiscountCategory(discount.DiscountCategory) != category {
			continue
		}
		if selected == nil || discount.EffectiveFrom.After(selected.EffectiveFrom) {
			selected = discount
		}
	}
	if !statusExists {
		variant.partialReason = fmt.Sprintf("Rail status %s has no imported discount record valid on the journey date.", statusCode)
		return variant
	}
	if selected == nil || selected.Indicator == "N" || selected.Indicator == "X" {
		return variant
	}
	price, applied, err := applyRailStatusDiscount(*selected, product, base)
	if err != nil {
		variant.partialReason = err.Error()
		return variant
	}
	if applied {
		variant.price = price
	}
	return variant
}

func applyRailStatusDiscount(discount ctdf.FareRailStatusDiscount, product ctdf.FareProduct, base ctdf.FareMoney) (ctdf.FareMoney, bool, error) {
	classIndex := -1
	switch product.Class {
	case ctdf.FareClassFirst:
		classIndex = 0
	case ctdf.FareClassStandard:
		classIndex = 2
	default:
		return base, false, fmt.Errorf("The ticket class is unavailable, so its rail discount could not be calculated.")
	}
	flat := discount.FirstSingleMaxFlat
	lowerMinimum, higherMinimum := discount.FirstLowerMinimum, discount.FirstHigherMinimum
	if classIndex == 2 {
		flat = discount.StandardSingleMaxFlat
		lowerMinimum, higherMinimum = discount.StandardLowerMinimum, discount.StandardHigherMinimum
	}
	if product.Type == ctdf.FareProductTypeReturn {
		classIndex++
		if product.Class == ctdf.FareClassFirst {
			flat = discount.FirstReturnMaxFlat
		} else {
			flat = discount.StandardReturnMaxFlat
		}
	} else if product.Type != ctdf.FareProductTypeSingleRide {
		return base, false, fmt.Errorf("The ticket type cannot use the imported single or return rail discount calculation.")
	}
	if discount.ClassMarkers != "" {
		marker := discount.ClassMarkers[classIndex]
		if marker == 'N' {
			return base, false, nil
		}
		if marker != 'Y' {
			return base, false, fmt.Errorf("The rail discount has an unsupported ticket-class marker.")
		}
	}
	if discount.Indicator == "F" {
		if flat <= 0 {
			return base, false, fmt.Errorf("The rail discount requires a flat fare that is missing from the imported record.")
		}
		return ctdf.FareMoney{AmountMinor: flat, Currency: base.Currency, Exponent: base.Exponent}, true, nil
	}
	if discount.Indicator != "0" && discount.Indicator != "M" && discount.Indicator != "H" && discount.Indicator != "L" {
		return base, false, fmt.Errorf("The rail discount indicator %q is not supported.", discount.Indicator)
	}
	if discount.PercentageTenths < 0 || discount.PercentageTenths > 1000 {
		return base, false, fmt.Errorf("The imported rail discount percentage is outside 0 to 100 percent.")
	}
	price := discountedRailAmount(base.AmountMinor, discount.PercentageTenths)
	switch discount.Indicator {
	case "M":
		if flat > 0 && price > flat {
			price = flat
		}
	case "H":
		if higherMinimum > 0 && price < higherMinimum {
			price = higherMinimum
		}
	case "L":
		if lowerMinimum > 0 && price < lowerMinimum {
			price = lowerMinimum
		}
	}
	if price < 0 {
		return base, false, fmt.Errorf("The calculated rail discount produced a negative fare.")
	}
	return ctdf.FareMoney{AmountMinor: price, Currency: base.Currency, Exponent: base.Exponent}, true, nil
}

func discountedRailAmount(amount int64, percentageTenths int) int64 {
	factor := int64(1000 - percentageTenths)
	return (amount/1000)*factor + ((amount%1000)*factor+500)/1000
}

func normalizedRailDiscountCategory(value string) string {
	value = strings.TrimSpace(value)
	number, err := strconv.Atoi(value)
	if err == nil {
		return strconv.Itoa(number)
	}
	return value
}

func fareCardStableIdentifier(card ctdf.FareRiderCategory) string {
	if card.RailcardCode != "" {
		return card.RailcardCode
	}
	return card.SourceIdentifier
}

func fareRailcardSupportsSinglePassenger(card ctdf.FareRiderCategory, age *int) bool {
	if card.MinimumPassengers > 1 || card.MinimumHolders > 1 || card.MinimumAdults > 1 || card.MinimumChildren > 1 ||
		card.RestrictedByArea || card.RestrictedByTrain || card.RestrictedByDate {
		return false
	}
	if age != nil {
		if *age < 16 && (card.MinimumAdults > 0 || card.HolderType == "A") {
			return false
		}
		if *age >= 16 && card.MinimumChildren > 0 {
			return false
		}
	}
	return true
}
