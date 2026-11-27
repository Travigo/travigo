package ctdf

import "testing"

func TestUserFareProfileValidate(t *testing.T) {
	age := 34
	valid := UserFareProfile{
		Age:           &age,
		DiscountCards: []FareCardSelection{{DatasetID: "rail", SourceIdentifier: "YNG"}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid profile error = %v", err)
	}

	duplicate := valid
	duplicate.DiscountCards = append(append([]FareCardSelection(nil), valid.DiscountCards...), valid.DiscountCards[0])
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate card was accepted")
	}

	badAge := 121
	if err := (UserFareProfile{Age: &badAge}).Validate(); err == nil {
		t.Fatal("age over 120 was accepted")
	}
	if err := (UserFareProfile{DiscountCards: []FareCardSelection{{DatasetID: "rail"}}}).Validate(); err == nil {
		t.Fatal("card selection without a source identifier was accepted")
	}
	if err := (UserFareProfile{DiscountCards: []FareCardSelection{
		{DatasetID: "netex", SourceIdentifier: "student", Kind: "fare_category"},
		{DatasetID: "netex", SourceIdentifier: "senior", Kind: "fare_category"},
	}}).Validate(); err == nil {
		t.Fatal("more than one passenger eligibility category was accepted")
	}
}
