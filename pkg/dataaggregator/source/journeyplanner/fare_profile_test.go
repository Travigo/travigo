package journeyplanner

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestSelectedFareProfileRiderRef(t *testing.T) {
	travelDate := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	minimumAge := 18
	catalogue := &fareCatalogue{
		datasetID: "netex-feed",
		riders: map[string]ctdf.FareRiderCategory{
			"student-ref": {
				FareRecord: ctdf.FareRecord{DatasetID: "netex-feed", SourceIdentifier: "student", EffectiveFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
				Name:       "Student",
				UserType:   "student",
				MinimumAge: &minimumAge,
			},
			"adult-ref": {
				FareRecord: ctdf.FareRecord{DatasetID: "netex-feed", SourceIdentifier: "adult"},
				Name:       "Adult",
				UserType:   "adult",
				IsDefault:  true,
			},
		},
	}
	selection := []ctdf.FareCardSelection{{DatasetID: "netex-feed", SourceIdentifier: "student", Kind: "fare_category"}}
	age := 25
	refs, assumption, reason := selectedFareProfileRiderRefs(catalogue, selection, &age, travelDate)
	if len(refs) != 1 || !refs["student-ref"] || assumption == "" || reason != "" {
		t.Fatalf("selected profile = refs %v, assumption %q, reason %q", refs, assumption, reason)
	}
	tooYoung := 16
	refs, _, reason = selectedFareProfileRiderRefs(catalogue, selection, &tooYoung, travelDate)
	if len(refs) != 0 || reason == "" {
		t.Fatalf("ineligible profile = refs %v, reason %q; want no selected category and a reason", refs, reason)
	}
}

func TestFareAgeDoesNotSelectProofBasedRiders(t *testing.T) {
	minimumAge := 18
	age := 20
	catalogue := &fareCatalogue{
		datasetID: "feed",
		riders: map[string]ctdf.FareRiderCategory{
			"adult":   {Name: "Adult", UserType: "adult", IsDefault: true},
			"student": {FareRecord: ctdf.FareRecord{SourceIdentifier: "student-source"}, Name: "Student", UserType: "student", MinimumAge: &minimumAge, ProofRequired: "Student ID"},
			"staff":   {FareRecord: ctdf.FareRecord{SourceIdentifier: "staff-source"}, Name: "Staff", UserType: "adult", IsDefault: true, MinimumAge: &minimumAge, ProofRequired: "Staff ID"},
			"senior":  {Name: "Senior", UserType: "senior", MinimumAge: &minimumAge},
		},
	}
	refs, _, _, _ := resolveFarePassenger(catalogue, "", &age, nil, time.Time{})
	if len(refs) != 1 || !refs["adult"] {
		t.Fatalf("age-only fare selected %v, want adult only", refs)
	}
	refs, _, _, _ = resolveFarePassenger(catalogue, "", nil, nil, time.Time{})
	if len(refs) != 1 || !refs["adult"] {
		t.Fatalf("default fare selected %v, want adult only", refs)
	}

	cards := []ctdf.FareCardSelection{{DatasetID: "feed", SourceIdentifier: "student-source", Kind: "fare_category"}}
	refs, _, _, reason := resolveFarePassenger(catalogue, "", &age, cards, time.Time{})
	if len(refs) != 1 || !refs["student"] || reason != "" {
		t.Fatalf("explicit category selected %v with reason %q, want student only", refs, reason)
	}

	cards[0].SourceIdentifier = "staff-source"
	refs, _, _, reason = resolveFarePassenger(catalogue, "", &age, cards, time.Time{})
	if len(refs) != 1 || !refs["staff"] || reason != "" {
		t.Fatalf("explicit proof-based adult category selected %v with reason %q, want staff only", refs, reason)
	}
}

func TestCombineFareLegsPricesTransferForPassenger(t *testing.T) {
	date := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	money := func(pence int64) ctdf.FareMoney {
		return ctdf.FareMoney{Currency: "GBP", Exponent: 2, AmountMinor: pence}
	}
	railProduct := ctdf.FareProduct{
		Name: "Rail transfer", RiderRef: "adult", Type: ctdf.FareProductTypeSingleRide, Class: ctdf.FareClassStandard,
		RailTicket: &ctdf.FareRailTicket{DiscountCategory: "02"},
	}
	catalogue := &fareCatalogue{
		datasetID: "feed",
		riders: map[string]ctdf.FareRiderCategory{
			"adult":   {Name: "Adult", UserType: "adult", IsDefault: true},
			"child":   {Name: "Child", UserType: "child"},
			"student": {FareRecord: ctdf.FareRecord{SourceIdentifier: "student-source"}, Name: "Student", UserType: "student"},
			"railcard": {
				FareRecord: ctdf.FareRecord{PrimaryIdentifier: "railcard", DatasetID: "feed"}, Name: "Example Railcard",
				UserType: "railcard", RailcardCode: "EXM", AdultStatusCode: "003",
			},
		},
		products: map[string]ctdf.FareProduct{
			"leg":              {Type: ctdf.FareProductTypeSingleRide},
			"adult-transfer":   {Name: "Adult transfer", RiderRef: "adult"},
			"child-transfer":   {Name: "Child transfer", RiderRef: "child"},
			"student-transfer": {Name: "Student transfer", RiderRef: "student"},
			"rail-transfer":    railProduct,
		},
		offers: map[string]ctdf.FareOffer{
			"adult-transfer":   {ProductRef: "adult-transfer", Price: money(500)},
			"child-transfer":   {ProductRef: "child-transfer", Price: money(250)},
			"student-transfer": {ProductRef: "student-transfer", Price: money(300)},
			"rail-transfer":    {ProductRef: "rail-transfer", Price: money(1000)},
		},
		railStatusDiscounts: []ctdf.FareRailStatusDiscount{{
			StatusCode: "003", DiscountCategory: "02", Indicator: "0", PercentageTenths: 300, ClassMarkers: "YYYY",
		}},
	}
	journey := &ctdf.Journey{DataSource: &ctdf.DataSourceReference{DatasetID: "feed"}, DepartureTimezone: "UTC"}
	items := []ctdf.JourneyPlanRouteItem{{Journey: journey, StartTime: date}, {Journey: journey, StartTime: date.Add(time.Hour)}}
	legMatches := [][]fareMatchedProduct{
		{{legGroupRef: "A", alternative: ctdf.FareQuoteAlternative{ProductRef: "leg", Price: money(100)}}},
		{{legGroupRef: "B", alternative: ctdf.FareQuoteAlternative{ProductRef: "leg", Price: money(100)}}},
	}
	for _, tt := range []struct {
		name        string
		age         int
		cards       []ctdf.FareCardSelection
		productRefs []string
		want        int64
	}{
		{name: "child", age: 10, productRefs: []string{"adult-transfer", "child-transfer"}, want: 450},
		{
			name: "student", age: 20,
			cards:       []ctdf.FareCardSelection{{DatasetID: "feed", SourceIdentifier: "student-source", Kind: "fare_category"}},
			productRefs: []string{"adult-transfer", "student-transfer"}, want: 500,
		},
		{
			name: "railcard", age: 30,
			cards:       []ctdf.FareCardSelection{{DatasetID: "feed", SourceIdentifier: "EXM", Kind: "discount_card"}},
			productRefs: []string{"rail-transfer"}, want: 900,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			catalogue.transferRules = []ctdf.FareTransferRule{{
				FromLegGroupRef: "A", ToLegGroupRef: "B", TransferType: "1", ProductRefs: tt.productRefs,
			}}
			got, reason := combineFareLegs(items, legMatches, map[string]*fareCatalogue{"feed": catalogue}, true, "", "", &tt.age, tt.cards)
			if reason != "" || len(got) == 0 || got[0].Price.AmountMinor != tt.want {
				t.Fatalf("combined fares = %#v, reason = %q, want least fare %d pence", got, reason, tt.want)
			}
		})
	}
}
