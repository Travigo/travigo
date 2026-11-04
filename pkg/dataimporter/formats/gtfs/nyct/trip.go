// Package nyct contains the NYC subway trip identity shared by GTFS import and realtime matching.
package nyct

import "regexp"

const SubwayDataset = "us-nyc-mta-subway-gtfs-schedule"

// The origin token is kept verbatim: it is hundredths of a minute and may be
// negative or exceed 24 hours. The path after N/S is optional and can disappear
// mid-trip. See https://www.mta.info/document/134521, pages 5–6.
var tripPattern = regexp.MustCompile(`^(?:.+_)?(-?[0-9]+)_([A-Za-z0-9]+\.{1,2}[NS])([A-Za-z0-9]*)$`)

func TripKey(tripID string) string {
	parts := tripPattern.FindStringSubmatch(tripID)
	if parts == nil {
		return ""
	}
	return parts[1] + "_" + parts[2]
}

// TripID removes the static schedule prefix while retaining the path. Distinct
// trains can share origin time, route and direction, so the path must be kept.
func TripID(tripID string) string {
	parts := tripPattern.FindStringSubmatch(tripID)
	if parts == nil {
		return ""
	}
	return parts[1] + "_" + parts[2] + parts[3]
}
