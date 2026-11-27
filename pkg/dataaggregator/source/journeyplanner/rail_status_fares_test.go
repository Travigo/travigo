package journeyplanner

import (
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestApplyRailStatusDiscount(t *testing.T) {
	product := ctdf.FareProduct{Type: ctdf.FareProductTypeSingleRide, Class: ctdf.FareClassStandard}
	base := ctdf.FareMoney{AmountMinor: 10000, Currency: "GBP", Exponent: 2}
	tests := []struct {
		name      string
		discount  ctdf.FareRailStatusDiscount
		want      int64
		applied   bool
		wantError bool
	}{
		{name: "percentage", discount: ctdf.FareRailStatusDiscount{Indicator: "0", PercentageTenths: 300, ClassMarkers: "YYYY"}, want: 7000, applied: true},
		{name: "flat", discount: ctdf.FareRailStatusDiscount{Indicator: "F", StandardSingleMaxFlat: 4500, ClassMarkers: "YYYY"}, want: 4500, applied: true},
		{name: "maximum", discount: ctdf.FareRailStatusDiscount{Indicator: "M", PercentageTenths: 300, StandardSingleMaxFlat: 6000, ClassMarkers: "YYYY"}, want: 6000, applied: true},
		{name: "higher minimum", discount: ctdf.FareRailStatusDiscount{Indicator: "H", PercentageTenths: 300, StandardHigherMinimum: 8000, ClassMarkers: "YYYY"}, want: 8000, applied: true},
		{name: "lower minimum", discount: ctdf.FareRailStatusDiscount{Indicator: "L", PercentageTenths: 300, StandardLowerMinimum: 7500, ClassMarkers: "YYYY"}, want: 7500, applied: true},
		{name: "class excluded", discount: ctdf.FareRailStatusDiscount{Indicator: "0", PercentageTenths: 300, ClassMarkers: "NNNY"}, want: 10000},
		{name: "bad class", discount: ctdf.FareRailStatusDiscount{Indicator: "0", PercentageTenths: 300, ClassMarkers: "YY?Y"}, want: 10000, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, applied, err := applyRailStatusDiscount(test.discount, product, base)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError %t", err, test.wantError)
			}
			if got.AmountMinor != test.want || applied != test.applied {
				t.Fatalf("price = %d, applied = %t; want %d, %t", got.AmountMinor, applied, test.want, test.applied)
			}
		})
	}
}

func TestRailFarePriceVariantsUseAgeAndOwnedRailcard(t *testing.T) {
	travelDate := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	catalogue := &fareCatalogue{railStatusDiscounts: []ctdf.FareRailStatusDiscount{
		{StatusCode: "001", DiscountCategory: "02", Indicator: "0", PercentageTenths: 500, ClassMarkers: "YYYY", FareRecord: ctdf.FareRecord{EffectiveFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), EffectiveTo: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{StatusCode: "003", DiscountCategory: "02", Indicator: "0", PercentageTenths: 300, ClassMarkers: "YYYY", FareRecord: ctdf.FareRecord{EffectiveFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), EffectiveTo: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	product := ctdf.FareProduct{
		Type:  ctdf.FareProductTypeSingleRide,
		Class: ctdf.FareClassStandard,
		RailTicket: &ctdf.FareRailTicket{
			DiscountCategory: "02",
		},
	}
	base := ctdf.FareMoney{AmountMinor: 10000, Currency: "GBP", Exponent: 2}
	adultAge := 25
	card := ctdf.FareRiderCategory{
		Name:            "Example Railcard",
		RailcardCode:    "EXM",
		AdultStatusCode: "003",
		EffectiveFrom:   time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		EffectiveTo:     time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	variants := railFarePriceVariants(catalogue, product, base, travelDate, &adultAge, []ctdf.FareRiderCategory{card}, false, false)
	if len(variants) != 2 {
		t.Fatalf("got %d fare variants, want base fare and one railcard fare", len(variants))
	}
	if variants[0].price.AmountMinor != 10000 || variants[1].price.AmountMinor != 7000 {
		t.Fatalf("variant prices = [%d %d], want [10000 7000]", variants[0].price.AmountMinor, variants[1].price.AmountMinor)
	}
	if variants[1].partialReason != "" {
		t.Fatalf("railcard variant is unexpectedly partial: %q", variants[1].partialReason)
	}

	childAge := 10
	variants = railFarePriceVariants(catalogue, product, base, travelDate, &childAge, nil, false, false)
	if len(variants) != 1 || variants[0].price.AmountMinor != 5000 {
		t.Fatalf("child fare variants = %#v, want one 5000-pence fare", variants)
	}
}

func TestRailFarePriceVariantsMarkUnsupportedCardRulesPartial(t *testing.T) {
	travelDate := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	catalogue := &fareCatalogue{}
	product := ctdf.FareProduct{
		Type:       ctdf.FareProductTypeSingleRide,
		Class:      ctdf.FareClassStandard,
		RailTicket: &ctdf.FareRailTicket{DiscountCategory: "02"},
	}
	base := ctdf.FareMoney{AmountMinor: 10000, Currency: "GBP", Exponent: 2}
	age := 25
	card := ctdf.FareRiderCategory{Name: "Example Railcard", RailcardCode: "EXM", AdultStatusCode: "003", MinimumPassengers: 2}
	variants := railFarePriceVariants(catalogue, product, base, travelDate, &age, []ctdf.FareRiderCategory{card}, false, false)
	if len(variants) != 2 || variants[1].price.AmountMinor != base.AmountMinor || variants[1].partialReason == "" {
		t.Fatalf("unsupported group card variant = %#v, want adult price with a partial reason", variants)
	}
}

func TestRailFareUsesAdultBaselineWithoutChildStatus(t *testing.T) {
	travelDate := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	catalogue := &fareCatalogue{riders: map[string]ctdf.FareRiderCategory{
		"adult": {Name: "Adult", UserType: "adult", IsDefault: true},
	}}
	product := ctdf.FareProduct{
		RiderRef: "adult", Type: ctdf.FareProductTypeSingleRide, Class: ctdf.FareClassStandard,
		RailTicket: &ctdf.FareRailTicket{DiscountCategory: "02"},
	}
	base := ctdf.FareMoney{AmountMinor: 10000, Currency: "GBP", Exponent: 2}
	for _, tc := range []struct {
		age    int
		reason string
	}{
		{age: 4, reason: "under-5"},
		{age: 10, reason: "status 001"},
	} {
		refs, _, _, _ := resolveFarePassenger(catalogue, "", &tc.age, nil, travelDate)
		if !fareRiderMatches(catalogue, product.RiderRef, refs, true, nil) {
			t.Fatalf("age %d could not use the adult rail fare as a starting price", tc.age)
		}
		if fareRiderMatches(catalogue, product.RiderRef, refs, false, nil) {
			t.Fatalf("age %d unexpectedly matched an adult-only non-rail fare", tc.age)
		}
		variants := railFarePriceVariants(catalogue, product, base, travelDate, &tc.age, nil, false, false)
		if len(variants) != 1 || variants[0].price != base || !strings.Contains(variants[0].partialReason, tc.reason) {
			t.Fatalf("age %d variants = %#v, want a partial adult-price fallback", tc.age, variants)
		}
	}
}

func TestNonStandardRailDiscountKeepsUndiscountedAdultPrice(t *testing.T) {
	travelDate := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	catalogue := &fareCatalogue{}
	product := ctdf.FareProduct{
		Type: ctdf.FareProductTypeSingleRide, Class: ctdf.FareClassStandard,
		RailTicket: &ctdf.FareRailTicket{DiscountCategory: "02"},
	}
	base := ctdf.FareMoney{AmountMinor: 10000, Currency: "GBP", Exponent: 2}
	adultAge, childAge := 25, 10
	card := ctdf.FareRiderCategory{Name: "Example Railcard", RailcardCode: "EXM", AdultStatusCode: "003"}

	for _, age := range []*int{nil, &adultAge} {
		variants := railFarePriceVariants(catalogue, product, base, travelDate, age, nil, true, false)
		if len(variants) != 1 || variants[0].price != base || variants[0].partialReason != "" {
			t.Fatalf("adult variants = %#v, want a complete published adult fare", variants)
		}
	}
	variants := railFarePriceVariants(catalogue, product, base, travelDate, &childAge, nil, true, false)
	if len(variants) != 1 || !strings.Contains(variants[0].partialReason, "non-standard discount") {
		t.Fatalf("child variants = %#v, want a partial fare", variants)
	}
	variants = railFarePriceVariants(catalogue, product, base, travelDate, &adultAge, []ctdf.FareRiderCategory{card}, true, false)
	if len(variants) != 2 || variants[0].partialReason != "" || !strings.Contains(variants[1].partialReason, "non-standard discount") {
		t.Fatalf("adult railcard variants = %#v, want complete adult price and partial railcard price", variants)
	}
}

func TestRailFarePriceVariantsApplyImportedCardMinimumFareWhenRequired(t *testing.T) {
	travelDate := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	catalogue := &fareCatalogue{
		railStatusDiscounts: []ctdf.FareRailStatusDiscount{{
			StatusCode: "003", DiscountCategory: "02", Indicator: "0", PercentageTenths: 300, ClassMarkers: "YYYY",
			FareRecord: ctdf.FareRecord{EffectiveFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), EffectiveTo: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)},
		}},
		railcardMinimumFares: []ctdf.FareRailcardMinimumFare{{
			FareRecord:   ctdf.FareRecord{EffectiveFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), EffectiveTo: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)},
			RailcardCode: "EXM", TicketCode: "SVS", AmountPence: 8000,
		}},
	}
	product := ctdf.FareProduct{Type: ctdf.FareProductTypeSingleRide, Class: ctdf.FareClassStandard, RailTicket: &ctdf.FareRailTicket{TicketCode: "SVS", DiscountCategory: "02"}}
	base := ctdf.FareMoney{AmountMinor: 10000, Currency: "GBP", Exponent: 2}
	age := 25
	card := ctdf.FareRiderCategory{Name: "Example Railcard", RailcardCode: "EXM", AdultStatusCode: "003"}

	variants := railFarePriceVariants(catalogue, product, base, travelDate, &age, []ctdf.FareRiderCategory{card}, false, true)
	if len(variants) != 2 || variants[1].price.AmountMinor != 8000 || variants[1].partialReason != "" {
		t.Fatalf("railcard minimum fare variant = %#v, want 8000 pence without a partial reason", variants)
	}
	if len(variants[1].conditions) < 2 || variants[1].conditions[len(variants[1].conditions)-1] != "Railcard minimum fare applied: 8000 pence" {
		t.Fatalf("minimum fare conditions = %v", variants[1].conditions)
	}

	catalogue.railcardMinimumFares = nil
	variants = railFarePriceVariants(catalogue, product, base, travelDate, &age, []ctdf.FareRiderCategory{card}, false, true)
	if len(variants) != 2 || variants[1].price.AmountMinor != base.AmountMinor || variants[1].partialReason == "" {
		t.Fatalf("missing minimum fare variant = %#v, want base fare with a partial reason", variants)
	}
}

func TestFareRailConditionsMarkMatchedMinimumFareRestriction(t *testing.T) {
	departure := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	journey := &ctdf.Journey{
		DepartureTimezone: "UTC",
		Calls: []ctdf.JourneyCall{
			{StopRef: "from", Departure: departure},
			{StopRef: "to", Arrival: departure.Add(time.Hour)},
		},
	}
	item := ctdf.JourneyPlanRouteItem{
		Journey:                     journey,
		JourneyOriginStopIndex:      0,
		JourneyDestinationStopIndex: 1,
		OriginStopRef:               "from",
		DestinationStopRef:          "to",
		StartTime:                   departure,
		ArrivalTime:                 departure.Add(time.Hour),
	}
	conditions := &ctdf.FareRailConditions{Restrictions: []ctdf.FareRailRestriction{{
		Code: "XX",
		TimeRestrictions: []ctdf.FareRailTimeRestriction{{
			Sequence: "1", OutRet: "O", TimeFrom: 540, TimeTo: 600, Event: "D", TimetableType: "T", MinimumFare: true,
		}},
	}}}
	minimumFareApplies := false
	matches, reason := fareRailConditionsMatch(nil, &fareCatalogue{}, conditions, journey, item, &minimumFareApplies)
	if !matches || reason != "" || !minimumFareApplies {
		t.Fatalf("fareRailConditionsMatch() = %t, %q, minimum %t; want true, empty, true", matches, reason, minimumFareApplies)
	}
}
