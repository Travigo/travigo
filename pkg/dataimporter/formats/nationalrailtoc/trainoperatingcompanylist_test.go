package nationalrailtoc

import "testing"

func TestConvertToCTDFLeavesTOCColourForDatasourceFallback(t *testing.T) {
	list := &TrainOperatingCompanyList{Companies: []TrainOperatingCompany{{
		AtocCode: "XX",
		Name:     "Example Rail",
	}}}

	_, services := list.convertToCTDFWithStopNameOverrides(nil)
	if len(services) != 1 {
		t.Fatalf("got %d services, want 1", len(services))
	}

	service := services[0]
	if service.BrandColour != "" {
		t.Errorf("BrandColour = %q, want empty so datasource fallback can apply", service.BrandColour)
	}
	if service.BrandIcon != "/icons/national-rail.svg" {
		t.Errorf("BrandIcon = %q, want National Rail icon", service.BrandIcon)
	}
	if service.BrandDisplayMode != "short" {
		t.Errorf("BrandDisplayMode = %q, want short", service.BrandDisplayMode)
	}
}
