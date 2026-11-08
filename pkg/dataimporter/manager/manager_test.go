package manager

import (
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func TestFilterImportReportOnlyIncludesSupportedObjects(t *testing.T) {
	report := filterImportReport(datasets.DataImportReport{
		ImportedStops:           1,
		ImportedStopGroups:      2,
		ImportedServices:        3,
		ImportedJourneys:        4,
		ImportedJourneyTracks:   7,
		ImportedOperators:       5,
		ImportedOperationGroups: 6,
	}, datasets.SupportedObjects{
		StopGroups:     true,
		Services:       true,
		Journeys:       true,
		JourneyTracks:  true,
		OperatorGroups: true,
	})

	if report.ImportedStops != 0 || report.ImportedStopGroups != 2 || report.ImportedServices != 3 || report.ImportedJourneys != 4 || report.ImportedJourneyTracks != 7 || report.ImportedOperators != 0 || report.ImportedOperationGroups != 6 {
		t.Fatalf("unexpected filtered report: %+v", report)
	}
}

func TestTfLRouteTracksIsARegisteredBatchDataset(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(workingDirectory)

	dataset, err := GetDataset("gb-tfl-route-tracks")
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Format != datasets.DataSetFormatTfLRouteTracks {
		t.Fatalf("format = %q", dataset.Format)
	}
	if dataset.ImportDestination != "" {
		t.Fatalf("import destination = %q, want normal database import", dataset.ImportDestination)
	}
	if dataset.DatasetSize != "enrichment" || !dataset.SupportedObjects.JourneyTracks {
		t.Fatalf("dataset is not configured as a journey-track enrichment import: %+v", dataset)
	}
	if dataset.RefreshInterval != 7*24*time.Hour {
		t.Fatalf("refresh interval = %s, want 168h", dataset.RefreshInterval)
	}
}

func TestOSMRailTracksIsARegisteredBatchDataset(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(workingDirectory)

	dataset, err := GetDataset("global-openstreetmap-gb-national-rail-tracks")
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Format != datasets.DataSetFormatOSMRailTracks {
		t.Fatalf("format = %q", dataset.Format)
	}
	if dataset.DatasetSize != "journey-enrichment" || !dataset.SupportedObjects.JourneyTracks {
		t.Fatalf("dataset is not configured as a journey-track enrichment import: %+v", dataset)
	}
	if dataset.LinkedDataset != "gb-nationalrail-timetable" {
		t.Fatalf("linked dataset = %q", dataset.LinkedDataset)
	}
	if dataset.RefreshInterval != 7*24*time.Hour {
		t.Fatalf("refresh interval = %s, want 168h", dataset.RefreshInterval)
	}
}

func TestTravelineOperatorMetadataOverridesAreScopedToExactOperators(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(workingDirectory)

	dataset, err := GetDataset("gb-traveline-noc")
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.OperatorMetadata) != 31 {
		t.Fatalf("operator metadata overrides = %d, want 31", len(dataset.OperatorMetadata))
	}
	var goNorthWestRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-138306") {
			goNorthWestRule = candidate
		}
	}
	goNorthWest := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-138306", Regions: []string{"UK:REGION:NORTHWEST", "UK:REGION:EASTMIDLANDS", "UK:REGION:NORTHWEST"}}
	goNorthWestRule.Apply(goNorthWest)
	if !slices.Equal(goNorthWest.Regions, []string{"UK:REGION:NORTHWEST"}) {
		t.Fatalf("Go North West regions = %v, want a single North West region", goNorthWest.Regions)
	}
	if goNorthWestRule.Matches("other", goNorthWest.PrimaryIdentifier) || goNorthWestRule.Matches("noc", "gb-nocid-unrelated") {
		t.Fatal("Go North West metadata override matched an unrelated dataset or operator")
	}
	for _, id := range []string{"gb-nocid-135887", "gb-nocid-135888"} {
		var centrebusRule datasets.OperatorMetadata
		for _, candidate := range dataset.OperatorMetadata {
			if candidate.Matches("noc", id) {
				centrebusRule = candidate
			}
		}
		if !centrebusRule.Matches("noc", id) {
			t.Fatalf("Centrebus override did not match %s", id)
		}
		operator := &ctdf.Operator{PrimaryIdentifier: id, Website: "http://www.centrebus.info", Email: "help@centrebus.com", PhoneNumber: "01164105050", Address: "43 Wenlock Way, Leicester, LE4 9HU"}
		centrebusRule.Apply(operator)
		if operator.Website != "https://www.centrebus.info/" || operator.Email != "help@centrebus.co.uk" || operator.PhoneNumber != "0116 410 5050" || operator.Address != "Customer Services Team, 43 Wenlock Way, Leicester, LE4 9HU" {
			t.Fatalf("Centrebus override for %s = %+v", id, operator)
		}
		if centrebusRule.Matches("other", id) || centrebusRule.Matches("noc", "gb-nocid-unrelated") {
			t.Fatalf("Centrebus override for %s matched an unrelated dataset or operator", id)
		}
	}
	var override datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-137625") {
			override = candidate
		}
	}
	if !override.Matches("noc", "gb-nocid-137625") {
		t.Fatal("Citylink override did not load from datasource YAML")
	}
	citylink := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-137625", PhoneNumber: "08705505050"}
	if !override.Matches("noc", citylink.PrimaryIdentifier) {
		t.Fatal("Citylink override did not match its configured operator")
	}
	override.Apply(citylink)
	if citylink.PhoneNumber != "0141 352 4444" {
		t.Fatalf("Citylink phone = %q, want 0141 352 4444", citylink.PhoneNumber)
	}
	if override.Matches("other", citylink.PrimaryIdentifier) || override.Matches("noc", "gb-nocid-else") {
		t.Fatal("Citylink override matched an unrelated dataset or operator")
	}
	var lothianRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-137004") {
			lothianRule = candidate
		}
	}
	if !lothianRule.Matches("noc", "gb-nocid-137004") {
		t.Fatal("Lothian override did not load from datasource YAML")
	}
	lothian := &ctdf.Operator{
		PrimaryIdentifier: "gb-nocid-137004",
		Regions:           []string{"UK:REGION:NORTHEAST", "UK:REGION:SCOTLAND"},
	}
	lothianRule.Apply(lothian)
	if len(lothian.Regions) != 1 || lothian.Regions[0] != "UK:REGION:SCOTLAND" {
		t.Fatalf("Lothian regions = %v, want only Scotland", lothian.Regions)
	}
	if lothian.Website != "https://www.lothianbuses.com/" || lothian.PhoneNumber != "01315556363" || lothian.Address != "55 Annandale Street, Edinburgh EH7 4AZ" || len(lothian.OtherNames) != 1 || lothian.OtherNames[0] != "Lothian Buses Limited" {
		t.Fatalf("Lothian metadata override = %+v", lothian)
	}
	var flixbusRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-138393") {
			flixbusRule = candidate
		}
	}
	flixbus := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-138393"}
	flixbusRule.Apply(flixbus)
	if flixbus.Email != "info@flixbus.co.uk" || flixbus.PhoneNumber != "+44 (0)1491 502156" || flixbus.Address == "" {
		t.Fatalf("FlixBus contact override = %+v", flixbus)
	}
	var nationalExpressRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-137214") {
			nationalExpressRule = candidate
		}
	}
	nationalExpress := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-137214", Website: "http://www.nationalexpress.com"}
	if !nationalExpressRule.Matches("noc", nationalExpress.PrimaryIdentifier) {
		t.Fatal("National Express override did not load from datasource YAML")
	}
	nationalExpressRule.Apply(nationalExpress)
	if nationalExpress.Website != "https://www.nationalexpress.com" || nationalExpress.PhoneNumber != "03717 81 81 81" || nationalExpress.Address != "National Express House, Birmingham Coach Station, Mill Lane, Digbeth, Birmingham B5 6DD" || nationalExpress.Email != "" {
		t.Fatalf("National Express contact override = %+v", nationalExpress)
	}
	if nationalExpressRule.Matches("other", nationalExpress.PrimaryIdentifier) || nationalExpressRule.Matches("noc", "gb-nocid-else") {
		t.Fatal("National Express override matched an unrelated dataset or operator")
	}
	var prestonBusRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-137430") {
			prestonBusRule = candidate
		}
	}
	prestonBus := &ctdf.Operator{
		PrimaryIdentifier: "gb-nocid-137430",
		Website:           "http://www.prestonbus.co.uk/",
		Email:             "customer.care@prestonbus.co.uk",
		PhoneNumber:       "01772253671",
		Regions:           []string{"UK:REGION:YORKSHIRE", "UK:REGION:NORTHWEST", "UK:REGION:NORTHWEST"},
	}
	if !prestonBusRule.Matches("noc", prestonBus.PrimaryIdentifier) {
		t.Fatal("Preston Bus override did not load from datasource YAML")
	}
	prestonBusRule.Apply(prestonBus)
	if prestonBus.Website != "https://www.prestonbus.co.uk/" || prestonBus.Email != "customer.care@rotala.co.uk" || prestonBus.PhoneNumber != "01772 253 671" || prestonBus.Address != "Preston Bus Limited, 221 Deepdale Road, Preston, PR1 6NY" || prestonBus.SocialMedia["Twitter"] != "@PrestonBus" {
		t.Fatalf("Preston Bus metadata override = %+v", prestonBus)
	}
	if !slices.Equal(prestonBus.Regions, []string{"UK:REGION:NORTHWEST"}) {
		t.Fatalf("Preston Bus regions = %v, want only North West", prestonBus.Regions)
	}
	if prestonBusRule.Matches("other", prestonBus.PrimaryIdentifier) || prestonBusRule.Matches("noc", "gb-nocid-else") {
		t.Fatal("Preston Bus override matched an unrelated dataset or operator")
	}
	var stagecoachRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-136332") {
			stagecoachRule = candidate
		}
	}
	stagecoach := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-136332", Address: "Customer Services, FREEPOST STAGECOACH"}
	if !stagecoachRule.Matches("noc", stagecoach.PrimaryIdentifier) {
		t.Fatal("Stagecoach East Scotland override did not load from datasource YAML")
	}
	stagecoachRule.Apply(stagecoach)
	if stagecoach.Website != "https://www.stagecoachbus.com/about/east-scotland" || stagecoach.Email != "customer.services@stagecoachbus.com" || stagecoach.PhoneNumber != "0345 241 8000" {
		t.Fatalf("Stagecoach East Scotland contact override = %+v", stagecoach)
	}
	if stagecoach.Address != "Customer Services, FREEPOST STAGECOACH" {
		t.Fatalf("Stagecoach address changed without verified customer-postal address: %q", stagecoach.Address)
	}
	wantOtherNames := []string{"Fife Scottish Omnibuses Limited", "Stagecoach in Fife", "Stagecoach in Perth", "Stagecoach Strathtay", "Rennies Coaches"}
	if !slices.Equal(stagecoach.OtherNames, wantOtherNames) {
		t.Fatalf("Stagecoach alternate names = %v, want %v", stagecoach.OtherNames, wantOtherNames)
	}
	if len(stagecoach.Regions) != 1 || stagecoach.Regions[0] != "UK:REGION:SCOTLAND" {
		t.Fatalf("Stagecoach East Scotland regions = %v, want only Scotland", stagecoach.Regions)
	}
	if stagecoachRule.Matches("other", stagecoach.PrimaryIdentifier) || stagecoachRule.Matches("noc", "gb-nocid-else") {
		t.Fatal("Stagecoach override matched an unrelated dataset or operator")
	}
	var westMidlandsRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-138032") {
			westMidlandsRule = candidate
		}
	}
	westMidlands := &ctdf.Operator{
		PrimaryIdentifier: "gb-nocid-138032",
		OtherIdentifiers:  []string{"gb-noc-TCVW", "gb-noc-TNXB"},
		PrimaryName:       "National Express West Midlands",
		OtherNames:        []string{"West Midlands Travel Ltd", "National Express Coventry", "Travel Coventry", "Birmingham Services"},
	}
	westMidlandsRule.Apply(westMidlands)
	wantWestMidlandsNames := []string{"National Express West Midlands", "West Midlands Travel Ltd", "National Express Coventry", "Travel Coventry", "Birmingham Services"}
	if !slices.Equal(westMidlands.OtherNames, wantWestMidlandsNames) {
		t.Fatalf("West Midlands alternate names = %v, want %v", westMidlands.OtherNames, wantWestMidlandsNames)
	}
	if westMidlands.PrimaryName != "West Midlands Bus" {
		t.Fatalf("West Midlands primary name = %q, want West Midlands Bus", westMidlands.PrimaryName)
	}
	if westMidlands.PrimaryIdentifier != "gb-nocid-138032" || !slices.Equal(westMidlands.OtherIdentifiers, []string{"gb-noc-TCVW", "gb-noc-TNXB"}) {
		t.Fatalf("West Midlands identity identifiers changed by metadata override: %+v", westMidlands)
	}
	var goNorthEastRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-136485") {
			goNorthEastRule = candidate
		}
	}
	goNorthEast := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-136485", Website: "http://www.gonortheast.co.uk/", Address: "Customer Services Team, FREEPOST NT2674, Gateshead NE8 1BR"}
	if !goNorthEastRule.Matches("noc", goNorthEast.PrimaryIdentifier) {
		t.Fatal("Go North East override did not load from datasource YAML")
	}
	goNorthEastRule.Apply(goNorthEast)
	if goNorthEast.Website != "https://www.gonortheast.co.uk" || goNorthEast.PhoneNumber != "0191 420 5050" || goNorthEast.Address != "Go North East, Customer Services, 117 Queen Street, Gateshead, Tyne and Wear, NE8 2UA" || goNorthEast.Email != "" {
		t.Fatalf("Go North East contact override = %+v", goNorthEast)
	}
	if goNorthEastRule.Matches("other", goNorthEast.PrimaryIdentifier) || goNorthEastRule.Matches("noc", "gb-nocid-else") {
		t.Fatal("Go North East override matched an unrelated dataset or operator")
	}
	var beeNetworkRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-138528") {
			beeNetworkRule = candidate
		}
	}
	beeNetwork := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-138528"}
	if !beeNetworkRule.Matches("noc", beeNetwork.PrimaryIdentifier) {
		t.Fatal("Bee Network override did not load from datasource YAML")
	}
	beeNetworkRule.Apply(beeNetwork)
	if beeNetwork.Website != "https://tfgm.com/the-bee-network" || beeNetwork.Email != "hello@beenetwork.com" || beeNetwork.PhoneNumber != "0161 244 1000" || beeNetwork.Address != "Transport for Greater Manchester, 2 Piccadilly Place, Manchester, M1 3BG" || beeNetwork.SocialMedia["Twitter"] != "@beenetwork" {
		t.Fatalf("Bee Network contact override = %+v", beeNetwork)
	}
	if beeNetworkRule.Matches("other", beeNetwork.PrimaryIdentifier) || beeNetworkRule.Matches("noc", "gb-nocid-else") {
		t.Fatal("Bee Network override matched an unrelated dataset or operator")
	}
	var nctRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-137284") {
			nctRule = candidate
		}
	}
	nct := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-137284", Website: "http://www.nctx.co.uk", Address: "Lower Parliament Street, Nottingham NG1 1GG"}
	if !nctRule.Matches("noc", nct.PrimaryIdentifier) {
		t.Fatal("Nottingham City Transport override did not load from datasource YAML")
	}
	nctRule.Apply(nct)
	if nct.Website != "https://www.nctx.co.uk" || nct.Address != "Nottingham City Transport, NCT Travel Centre, 28-30 Lister Gate, Nottingham, NG1 7DD" {
		t.Fatalf("Nottingham City Transport override = %+v", nct)
	}
	if nct.Email != "" || nct.PhoneNumber != "" || nctRule.Matches("other", nct.PrimaryIdentifier) || nctRule.Matches("noc", "gb-nocid-else") {
		t.Fatal("Nottingham City Transport override changed unrelated contact fields or matched an unrelated record")
	}
	var arrivaNorthWestRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-135637") {
			arrivaNorthWestRule = candidate
		}
	}
	arrivaNorthWest := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-135637"}
	if !arrivaNorthWestRule.Matches("noc", arrivaNorthWest.PrimaryIdentifier) {
		t.Fatal("Arriva North West override did not load from datasource YAML")
	}
	arrivaNorthWestRule.Apply(arrivaNorthWest)
	if arrivaNorthWest.Website != "https://www.arrivabus.co.uk/locations/north-west" || arrivaNorthWest.PhoneNumber != "0344 800 44 11" || arrivaNorthWest.Address != "Arriva Customer Services, 487 Dunstable Road, FREEPOST ANG7624, Luton, Bedfordshire, LU4 8DS" {
		t.Fatalf("Arriva North West override = %+v", arrivaNorthWest)
	}
	if arrivaNorthWest.Email != "" || arrivaNorthWestRule.Matches("other", arrivaNorthWest.PrimaryIdentifier) || arrivaNorthWestRule.Matches("noc", "gb-nocid-else") {
		t.Fatal("Arriva North West override changed an unverified email or matched an unrelated record")
	}
	var arrivaMidlandsRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-135638") {
			arrivaMidlandsRule = candidate
		}
	}
	arrivaMidlands := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-135638"}
	if !arrivaMidlandsRule.Matches("noc", arrivaMidlands.PrimaryIdentifier) {
		t.Fatal("Arriva Midlands override did not load from datasource YAML")
	}
	arrivaMidlandsRule.Apply(arrivaMidlands)
	if arrivaMidlands.Website != "https://www.arrivabus.co.uk/locations/midlands" || arrivaMidlands.PhoneNumber != "0344 800 44 11" || arrivaMidlands.Address != "Arriva Customer Services, 487 Dunstable Road, FREEPOST ANG7624, Luton, Bedfordshire, LU4 8DS" {
		t.Fatalf("Arriva Midlands override = %+v", arrivaMidlands)
	}
	if arrivaMidlands.Email != "" || arrivaMidlandsRule.Matches("other", arrivaMidlands.PrimaryIdentifier) || arrivaMidlandsRule.Matches("noc", "gb-nocid-else") {
		t.Fatal("Arriva Midlands override changed an unverified email or matched an unrelated record")
	}
	for _, tc := range []struct {
		id, website, address string
	}{
		{"gb-nocid-136340", "", "First West of England Ltd, Enterprise House, Easton Road, Bristol, BS5 0DZ"},
		{"gb-nocid-136351", "", "First Essex Buses Limited, First Bus, Westway, Chelmsford, Essex CM1 3AR"},
		{"gb-nocid-136354", "https://www.firstbus.co.uk/wessex-dorset-south-somerset", ""},
	} {
		var rule datasets.OperatorMetadata
		for _, candidate := range dataset.OperatorMetadata {
			if candidate.Matches("noc", tc.id) {
				rule = candidate
			}
		}
		operator := &ctdf.Operator{PrimaryIdentifier: tc.id}
		if !rule.Matches("noc", operator.PrimaryIdentifier) {
			t.Fatalf("First Bus override for %s did not load from datasource YAML", tc.id)
		}
		rule.Apply(operator)
		if operator.Website != tc.website || operator.Address != tc.address || operator.Email != "" || operator.PhoneNumber != "" {
			t.Fatalf("First Bus override for %s = %+v", tc.id, operator)
		}
		if rule.Matches("other", tc.id) || rule.Matches("noc", "gb-nocid-unrelated") {
			t.Fatalf("First Bus override for %s matched an unrelated dataset or operator", tc.id)
		}
	}
	var diamondNorthWestRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-136521") {
			diamondNorthWestRule = candidate
		}
	}
	diamondNorthWest := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-136521", Website: "http://www.diamondbusnorthwest.co.uk/", Email: "comments@diamondbuses.com"}
	if !diamondNorthWestRule.Matches("noc", diamondNorthWest.PrimaryIdentifier) {
		t.Fatal("Diamond Bus North West override did not load from datasource YAML")
	}
	diamondNorthWestRule.Apply(diamondNorthWest)
	if diamondNorthWest.Website != "https://www.diamondbuses.com/north-west/" || diamondNorthWest.Email != "commentsdiamondbusnorthwest@rotala.co.uk" || diamondNorthWest.PhoneNumber != "01204 937 535" || diamondNorthWest.Address != "Customer Services, Diamond Bus North West, 13 Silverwell Street, Bolton, BL1 1PP" || diamondNorthWest.SocialMedia["Twitter"] != "@DiamondBusNW" {
		t.Fatalf("Diamond Bus North West override = %+v", diamondNorthWest)
	}
	if diamondNorthWestRule.Matches("other", diamondNorthWest.PrimaryIdentifier) || diamondNorthWestRule.Matches("noc", "gb-nocid-unrelated") {
		t.Fatal("Diamond Bus North West override matched an unrelated dataset or operator")
	}
	var cardiffBusRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-135848") {
			cardiffBusRule = candidate
		}
	}
	cardiffBus := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-135848", Website: "http://www.cardiffbus.com"}
	if !cardiffBusRule.Matches("noc", cardiffBus.PrimaryIdentifier) {
		t.Fatal("Cardiff Bus override did not load from datasource YAML")
	}
	cardiffBusRule.Apply(cardiffBus)
	if cardiffBus.Website != "https://www.cardiffbus.com" || cardiffBus.Address != "Cardiff Bus, Leckwith Depot & Offices, Sloper Road, Leckwith, Cardiff, CF11 8TB" {
		t.Fatalf("Cardiff Bus override = %+v", cardiffBus)
	}
	if cardiffBus.Email != "" || cardiffBus.PhoneNumber != "" || cardiffBus.SocialMedia != nil || cardiffBusRule.Matches("other", cardiffBus.PrimaryIdentifier) || cardiffBusRule.Matches("noc", "gb-nocid-unrelated") {
		t.Fatal("Cardiff Bus override changed existing contact details or matched an unrelated record")
	}
	var stagecoachSouthWalesRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-137469") {
			stagecoachSouthWalesRule = candidate
		}
	}
	stagecoachSouthWales := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-137469", Website: "http://www.stagecoachbus.com/regional-help-and-contact/south-wales", Email: "southwales.enquiries@stagecoachbus.com", PhoneNumber: "03452418000", Address: "", SocialMedia: map[string]string{"Twitter": "@StagecoachWales"}}
	if !stagecoachSouthWalesRule.Matches("noc", stagecoachSouthWales.PrimaryIdentifier) {
		t.Fatal("Stagecoach South Wales override did not load from datasource YAML")
	}
	stagecoachSouthWalesRule.Apply(stagecoachSouthWales)
	if stagecoachSouthWales.Website != "https://www.stagecoachbus.com/about/south-wales" || stagecoachSouthWales.Email != "southwales.enquiries@stagecoachbus.com" || stagecoachSouthWales.PhoneNumber != "03452418000" || stagecoachSouthWales.Address != "" || stagecoachSouthWales.SocialMedia["Twitter"] != "@StagecoachWales" {
		t.Fatalf("Stagecoach South Wales override = %+v", stagecoachSouthWales)
	}
	if stagecoachSouthWalesRule.Matches("other", stagecoachSouthWales.PrimaryIdentifier) || stagecoachSouthWalesRule.Matches("noc", "gb-nocid-unrelated") {
		t.Fatal("Stagecoach South Wales override matched an unrelated dataset or operator")
	}
	var unibusRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-138286") {
			unibusRule = candidate
		}
	}
	unibus := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-138286", Website: "https://www.unibuses.co.uk", Email: "managersmailbox@bluestarbus.co.uk", PhoneNumber: "01202338420", Address: "Barton Park, FREEPOST SO3313, Eastleigh"}
	if !unibusRule.Matches("noc", unibus.PrimaryIdentifier) {
		t.Fatal("Unibus override did not load from datasource YAML")
	}
	unibusRule.Apply(unibus)
	if unibus.Website != "https://www.unibuses.co.uk" || unibus.Email != "managersmailbox@bluestarbus.co.uk" || unibus.PhoneNumber != "01202 338 420" || unibus.Address != "Go South Coast Ltd, Customer Services Manager, Towngate House, 2-8 Parkstone Road, Poole, BH15 2PR" {
		t.Fatalf("Unibus override = %+v", unibus)
	}
	if unibusRule.Matches("other", unibus.PrimaryIdentifier) || unibusRule.Matches("noc", "gb-nocid-unrelated") {
		t.Fatal("Unibus override matched an unrelated dataset or operator")
	}
	var stagecoachEastMidlandsRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-136971") {
			stagecoachEastMidlandsRule = candidate
		}
	}
	stagecoachEastMidlands := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-136971", Website: "http://www.stagecoachbus.com/regional-help-and-contact/east-midlands", Email: "eastmidlands.enquiries@stagecoachbus.com", PhoneNumber: "03452418000", Address: "Warneford House, Runcorn Road, Lincoln LN6 3QP", SocialMedia: map[string]string{"Twitter": "@StagecoachEMid"}}
	if !stagecoachEastMidlandsRule.Matches("noc", stagecoachEastMidlands.PrimaryIdentifier) {
		t.Fatal("Stagecoach East Midlands override did not load from datasource YAML")
	}
	stagecoachEastMidlandsRule.Apply(stagecoachEastMidlands)
	if stagecoachEastMidlands.Website != "https://www.stagecoachbus.com/about/east-midlands" || stagecoachEastMidlands.Email != "eastmidlands.enquiries@stagecoachbus.com" || stagecoachEastMidlands.PhoneNumber != "0345 241 8000" || stagecoachEastMidlands.Address != "Customer Services, FREEPOST STAGECOACH" || stagecoachEastMidlands.SocialMedia["Twitter"] != "@StagecoachEMid" {
		t.Fatalf("Stagecoach East Midlands override = %+v", stagecoachEastMidlands)
	}
	if stagecoachEastMidlandsRule.Matches("other", stagecoachEastMidlands.PrimaryIdentifier) || stagecoachEastMidlandsRule.Matches("noc", "gb-nocid-unrelated") {
		t.Fatal("Stagecoach East Midlands override matched an unrelated dataset or operator")
	}
	var stagecoachSouthEastRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-136221") {
			stagecoachSouthEastRule = candidate
		}
	}
	stagecoachSouthEast := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-136221", Website: "http://www.stagecoachbus.com/regional-help-and-contact/south-east", Email: "southeast.enquiries@stagecoachbus.com", PhoneNumber: "03452418000", SocialMedia: map[string]string{"Twitter": "@StagecoachSE"}}
	if !stagecoachSouthEastRule.Matches("noc", stagecoachSouthEast.PrimaryIdentifier) {
		t.Fatal("Stagecoach South East override did not load from datasource YAML")
	}
	stagecoachSouthEastRule.Apply(stagecoachSouthEast)
	if stagecoachSouthEast.Website != "https://www.stagecoachbus.com/about/south-east" || stagecoachSouthEast.Email != "southeast.enquiries@stagecoachbus.com" || stagecoachSouthEast.PhoneNumber != "0345 241 8000" || stagecoachSouthEast.Address != "" || stagecoachSouthEast.SocialMedia["Twitter"] != "@StagecoachSE" {
		t.Fatalf("Stagecoach South East override = %+v", stagecoachSouthEast)
	}
	if stagecoachSouthEastRule.Matches("other", stagecoachSouthEast.PrimaryIdentifier) || stagecoachSouthEastRule.Matches("noc", "gb-nocid-unrelated") {
		t.Fatal("Stagecoach South East override matched an unrelated dataset or operator")
	}
	var stagecoachMerseysideRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-136477") {
			stagecoachMerseysideRule = candidate
		}
	}
	stagecoachMerseyside := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-136477", Website: "https://www.stagecoachbus.com/regional-help-and-contact/merseyside-and-south-lancashire", Email: "enquiries.merseyside@stagecoachbus.com", PhoneNumber: "03452418000", Address: "Head Office, Gillmoss Bus Depot, East Lancashire Road, Liverpool L11 0BB"}
	if !stagecoachMerseysideRule.Matches("noc", stagecoachMerseyside.PrimaryIdentifier) {
		t.Fatal("Stagecoach Merseyside and South Lancashire override did not load from datasource YAML")
	}
	stagecoachMerseysideRule.Apply(stagecoachMerseyside)
	if stagecoachMerseyside.Website != "https://www.stagecoachbus.com/about/merseyside-and-south-lancashire" || stagecoachMerseyside.Email != "enquiries.merseyside@stagecoachbus.com" || stagecoachMerseyside.PhoneNumber != "0345 241 8000" || stagecoachMerseyside.Address != "Customer Services, FREEPOST STAGECOACH" {
		t.Fatalf("Stagecoach Merseyside and South Lancashire override = %+v", stagecoachMerseyside)
	}
	if stagecoachMerseysideRule.Matches("other", stagecoachMerseyside.PrimaryIdentifier) || stagecoachMerseysideRule.Matches("noc", "gb-nocid-unrelated") {
		t.Fatal("Stagecoach Merseyside and South Lancashire override matched an unrelated dataset or operator")
	}
	var stagecoachManchesterRule datasets.OperatorMetadata
	for _, candidate := range dataset.OperatorMetadata {
		if candidate.Matches("noc", "gb-nocid-136515") {
			stagecoachManchesterRule = candidate
		}
	}
	stagecoachManchester := &ctdf.Operator{PrimaryIdentifier: "gb-nocid-136515", Website: "http://www.stagecoachbus.com/regional-help-and-contact/greater-manchester", Email: "manchester.enquiries@stagecoacbus.com", PhoneNumber: "03452418000", Address: "Hyde Road, Manchester M12 6JS"}
	if !stagecoachManchesterRule.Matches("noc", stagecoachManchester.PrimaryIdentifier) {
		t.Fatal("Stagecoach Manchester override did not load from datasource YAML")
	}
	stagecoachManchesterRule.Apply(stagecoachManchester)
	if stagecoachManchester.Website != "https://www.stagecoachbus.com/about/manchester" || stagecoachManchester.Email != "manchester.enquiries@stagecoacbus.com" || stagecoachManchester.PhoneNumber != "0161 244 1000" || stagecoachManchester.Address != "Hyde Road, Manchester M12 6JS" {
		t.Fatalf("Stagecoach Manchester override = %+v", stagecoachManchester)
	}
	if stagecoachManchesterRule.Matches("other", stagecoachManchester.PrimaryIdentifier) || stagecoachManchesterRule.Matches("noc", "gb-nocid-unrelated") {
		t.Fatal("Stagecoach Manchester override matched an unrelated dataset or operator")
	}
}

func TestShouldSkipFreshDataset(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	dataset := &datasets.DataSet{RefreshInterval: 7 * 24 * time.Hour}

	tests := []struct {
		name    string
		version *ctdf.DatasetVersion
		force   bool
		want    bool
	}{
		{
			name:    "fresh version",
			version: &ctdf.DatasetVersion{LastModified: now.Add(-6 * 24 * time.Hour)},
			want:    true,
		},
		{
			name:    "version exactly due",
			version: &ctdf.DatasetVersion{LastModified: now.Add(-7 * 24 * time.Hour)},
		},
		{
			name:    "stale version",
			version: &ctdf.DatasetVersion{LastModified: now.Add(-8 * 24 * time.Hour)},
		},
		{
			name:    "forced fresh version",
			version: &ctdf.DatasetVersion{LastModified: now.Add(-time.Hour)},
			force:   true,
		},
		{
			name: "no version",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldSkipFreshDataset(dataset, test.version, test.force, now); got != test.want {
				t.Fatalf("shouldSkipFreshDataset() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestTempDownloadFileReturnsTLSFailure(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()

	_, _, _, err := tempDownloadFile(&datasets.DataSet{Source: server.URL}, "")
	if err == nil {
		t.Fatal("tempDownloadFile() error = nil, want TLS error")
	}
}

func TestTempDownloadFileReturnsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
	}))
	defer server.Close()

	_, _, _, err := tempDownloadFile(&datasets.DataSet{Source: server.URL}, "")
	if err == nil {
		t.Fatal("tempDownloadFile() error = nil, want HTTP error")
	}
}
