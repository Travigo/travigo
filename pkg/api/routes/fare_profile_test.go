package routes

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestFareProfileOptionKind(t *testing.T) {
	tests := []struct {
		name string
		item ctdf.FareRiderCategory
		kind string
		want bool
	}{
		{name: "railcard", item: ctdf.FareRiderCategory{UserType: "railcard"}, kind: "discount_card", want: true},
		{name: "sale discount right", item: ctdf.FareRiderCategory{UserType: "discount_card", CardType: "SaleDiscountRight: travelCard"}, kind: "discount_card", want: true},
		{name: "student category", item: ctdf.FareRiderCategory{UserType: "student", Name: "Student"}, kind: "fare_category", want: true},
		{name: "named GTFS category", item: ctdf.FareRiderCategory{Name: "Concession"}, kind: "fare_category", want: true},
		{name: "default adult", item: ctdf.FareRiderCategory{UserType: "adult", Name: "Adult", IsDefault: true}, want: false},
		{name: "age-only child", item: ctdf.FareRiderCategory{UserType: "child", Name: "Child"}, want: false},
		{name: "adult with proof", item: ctdf.FareRiderCategory{UserType: "adult", Name: "Staff adult", IsDefault: true, ProofRequired: "Staff ID"}, kind: "fare_category", want: true},
		{name: "child with eligibility URL", item: ctdf.FareRiderCategory{UserType: "child", Name: "School pupil", EligibilityURL: "https://example.com/school-eligibility"}, kind: "fare_category", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, got := fareProfileOptionKind(test.item)
			if got != test.want || kind != test.kind {
				t.Fatalf("fareProfileOptionKind() = %q, %t; want %q, %t", kind, got, test.kind, test.want)
			}
		})
	}
}

func TestFareCardRecordValidOnIncludesEndDate(t *testing.T) {
	rider := ctdf.FareRiderCategory{FareRecord: ctdf.FareRecord{
		EffectiveFrom: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		EffectiveTo:   time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC),
	}}
	if !fareCardRecordValidOn(rider, time.Date(2026, 11, 27, 14, 30, 0, 0, time.UTC)) {
		t.Fatal("record's inclusive end date was treated as expired at midnight")
	}
	if fareCardRecordValidOn(rider, time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("record was accepted after its inclusive end date")
	}
}
