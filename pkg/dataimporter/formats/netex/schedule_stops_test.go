package netex

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func TestParseArchiveIndexesStopsAcrossEntries(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entries := map[string]string{
		"stops.xml":     `<PublicationDelivery xmlns="http://www.netex.org.uk/netex"><dataObjects><StopPlace id="place"><Name>Central</Name><Centroid><Location><Longitude>-1.2</Longitude><Latitude>52.1</Latitude></Location></Centroid></StopPlace><Quay id="quay"><Name>Bay 4</Name><ParentZoneRef ref="place"/><PublicCode>4</PublicCode></Quay><ScheduledStopPoint id="ssp"><Name>Central</Name></ScheduledStopPoint><PassengerStopAssignment id="assignment"><ScheduledStopPointRef ref="ssp"/><QuayRef ref="quay"/></PassengerStopAssignment></dataObjects></PublicationDelivery>`,
		"timetable.xml": `<PublicationDelivery xmlns="http://www.netex.org.uk/netex"><dataObjects><Line id="line"><Name>Route 1</Name></Line></dataObjects></PublicationDelivery>`,
		"metadata.json": `not xml`,
	}
	for name, contents := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	var schedule Schedule
	if err := schedule.ParseArchive(bytes.NewReader(archive.Bytes()), int64(archive.Len())); err != nil {
		t.Fatalf("ParseArchive() error = %v", err)
	}
	if len(schedule.lines) != 1 {
		t.Fatalf("indexed %d lines, want 1", len(schedule.lines))
	}
	dataset := datasets.DataSet{Identifier: "schedule", LinkedDataset: "stops", SupportedObjects: datasets.SupportedObjects{Stops: false}}
	id, name, err := schedule.resolveStop(dataset, "ssp")
	if err != nil {
		t.Fatalf("resolveStop() error = %v", err)
	}
	if id != "stops-stop-quay" || name != "Bay 4" {
		t.Fatalf("resolveStop() = (%q, %q), want (%q, %q)", id, name, "stops-stop-quay", "Bay 4")
	}
}

func TestConvertStopsBuildsLocationsAliasesAndAssociations(t *testing.T) {
	const xmlDocument = `<PublicationDelivery xmlns="http://www.netex.org.uk/netex"><dataObjects><StopPlace id="place"><Name>Central</Name><StopPlaceType>busStation</StopPlaceType><KeyList><KeyValue><Key>ATCOCode</Key><Value>123456</Value></KeyValue></KeyList></StopPlace><Quay id="quay"><Name>Bay 4</Name><PublicCode>4</PublicCode><ParentZoneRef ref="place"/><Centroid><Location><Longitude>-1.2</Longitude><Latitude>52.1</Latitude></Location></Centroid></Quay></dataObjects></PublicationDelivery>`
	var schedule Schedule
	if err := schedule.ParseFile(strings.NewReader(xmlDocument)); err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	stops, groups, err := schedule.convertStops(datasets.DataSet{Identifier: "netex"}, &ctdf.DataSourceReference{}, now)
	if err != nil {
		t.Fatalf("convertStops() error = %v", err)
	}
	if len(stops) != 2 || len(groups) != 1 {
		t.Fatalf("convertStops() returned %d stops and %d groups, want 2 and 1", len(stops), len(groups))
	}
	byID := map[string]*ctdf.Stop{}
	for _, stop := range stops {
		byID[stop.PrimaryIdentifier] = stop
	}
	quay := byID["netex-stop-quay"]
	if quay == nil || quay.PrimaryName != "Bay 4" || quay.Location == nil {
		t.Fatalf("converted quay = %#v", quay)
	}
	if got := quay.Location.Coordinates; len(got) != 2 || got[0] != -1.2 || got[1] != 52.1 {
		t.Fatalf("quay coordinates = %v, want [-1.2 52.1]", got)
	}
	if len(quay.Associations) != 2 || quay.Associations[0].AssociatedIdentifier != "netex-stop-place" || quay.Associations[1].AssociatedIdentifier != "netex-stopgroup-place" {
		t.Fatalf("quay associations = %#v", quay.Associations)
	}
	if groups[0].PrimaryIdentifier != "netex-stopgroup-place" {
		t.Fatalf("stop group ID = %q", groups[0].PrimaryIdentifier)
	}
	place := byID["netex-stop-place"]
	if place == nil || len(place.OtherIdentifiers) != 1 || place.OtherIdentifiers[0] != "gb-atco-123456" {
		t.Fatalf("converted place aliases = %#v", place)
	}
}

func TestParseRejectsEmptyAndUnrecognizedXML(t *testing.T) {
	for _, input := range []string{"", `<html><body>no data</body></html>`, `<SomeDocument><Anything/></SomeDocument>`} {
		var schedule Schedule
		if err := schedule.ParseFile(strings.NewReader(input)); err == nil {
			t.Errorf("ParseFile(%q) succeeded, want error", input)
		}
	}
	var schedule Schedule
	if err := schedule.ParseFile(strings.NewReader(`<PublicationDelivery><dataObjects><StopPlace id="unterminated">`)); err == nil {
		t.Error("ParseFile() accepted malformed XML")
	}
}

func TestNestedQuaysAndLinkedExternalAssignment(t *testing.T) {
	const document = `<PublicationDelivery xmlns="http://www.netex.org.uk/netex"><dataObjects><StopPlace id="place"><Name>Station</Name><quays><Quay id="nested-quay"><Name>Platform</Name></Quay><Quay id="explicit-quay"><ParentZoneRef ref="place"/></Quay></quays></StopPlace><PassengerStopAssignment id="external-assignment"><ScheduledStopPointRef ref="external-ssp"/><QuayRef ref="external-quay"/></PassengerStopAssignment></dataObjects></PublicationDelivery>`
	var schedule Schedule
	if err := schedule.ParseFile(strings.NewReader(document)); err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if schedule.quays["nested-quay"] == nil || schedule.quayParents["nested-quay"] != "place" {
		t.Fatalf("nested quay hierarchy = %#v, parents = %#v", schedule.quays, schedule.quayParents)
	}
	if parent := schedule.placeParents["place"]; parent != "" {
		t.Fatalf("stop place inherited its quay's parent reference %q", parent)
	}
	dataset := datasets.DataSet{Identifier: "schedule", LinkedDataset: "stops"}
	id, name, err := schedule.resolveStop(dataset, "external-ssp")
	if err != nil {
		t.Fatalf("resolveStop() error = %v", err)
	}
	if id != "stops-stop-external-quay" || name != "" {
		t.Fatalf("external resolveStop() = (%q, %q), want linked ID and empty name", id, name)
	}
	if id, _, err := schedule.resolveStop(dataset, "unmapped-ssp"); err != nil || id != "" {
		t.Fatalf("unmapped SSP resolveStop() = (%q, %v), want empty result", id, err)
	}
}

func TestParseRejectsConflictingPassengerStopAssignments(t *testing.T) {
	const document = `<PublicationDelivery xmlns="http://www.netex.org.uk/netex"><dataObjects><PassengerStopAssignment id="one"><ScheduledStopPointRef ref="ssp"/><QuayRef ref="quay-one"/></PassengerStopAssignment><PassengerStopAssignment id="two"><ScheduledStopPointRef ref="ssp"/><QuayRef ref="quay-two"/></PassengerStopAssignment></dataObjects></PublicationDelivery>`
	var schedule Schedule
	if err := schedule.ParseFile(strings.NewReader(document)); err == nil || !strings.Contains(err.Error(), "conflicting PassengerStopAssignments") {
		t.Fatalf("ParseFile() error = %v, want conflicting assignment error", err)
	}
}
