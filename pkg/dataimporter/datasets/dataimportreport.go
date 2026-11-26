package datasets

import "time"

const maxFareValidityGapExamples = 50

type DataImportReport struct {
	DatasetIdentifier      string
	DatasetProvidedVersion string `bson:",omitempty"`

	CreationDateTime time.Time     `groups:"web-import-report"`
	RunTime          time.Duration `groups:"web-import-report"`

	ImportedStops                  int `groups:"web-import-report"`
	ImportedStopGroups             int `groups:"web-import-report"`
	ImportedServices               int `groups:"web-import-report"`
	ImportedJourneys               int `groups:"web-import-report"`
	ImportedJourneyTracks          int
	ImportedOperators              int      `groups:"web-import-report"`
	ImportedOperationGroups        int      `groups:"web-import-report"`
	FareDataPresent                bool     `groups:"web-import-report"`
	ImportedFareLocations          int      `groups:"web-import-report"`
	FareLocationsLinkedToStops     int      `groups:"web-import-report"`
	FareLocationsWithoutStopLink   int      `groups:"web-import-report"`
	ImportedFareProducts           int      `groups:"web-import-report"`
	ImportedFareOffers             int      `groups:"web-import-report"`
	ImportedFareRules              int      `groups:"web-import-report"`
	ImportedFareMedia              int      `groups:"web-import-report"`
	ImportedFareRiderCategories    int      `groups:"web-import-report"`
	ImportedFareTariffs            int      `groups:"web-import-report"`
	ImportedFareNetworks           int      `groups:"web-import-report"`
	ImportedFareRouteNetworks      int      `groups:"web-import-report"`
	ImportedFareTimeframes         int      `groups:"web-import-report"`
	ImportedFareLegJoins           int      `groups:"web-import-report"`
	ImportedFareTransferRules      int      `groups:"web-import-report"`
	UncoveredFareValidityIntervals int      `groups:"web-import-report"`
	FareValidityGapDetails         []string `groups:"web-import-report" bson:",omitempty" json:"fare_validity_gap_details,omitempty"`
}

func (report *DataImportReport) AddFareValidityGap(detail string) {
	if report == nil {
		return
	}
	report.UncoveredFareValidityIntervals++
	if len(report.FareValidityGapDetails) < maxFareValidityGapExamples {
		report.FareValidityGapDetails = append(report.FareValidityGapDetails, detail)
	}
}

func (report *DataImportReport) MergeFareValidityGaps(other DataImportReport) {
	if report == nil {
		return
	}
	report.UncoveredFareValidityIntervals += other.UncoveredFareValidityIntervals
	remaining := maxFareValidityGapExamples - len(report.FareValidityGapDetails)
	if remaining > 0 && len(other.FareValidityGapDetails) > 0 {
		if remaining > len(other.FareValidityGapDetails) {
			remaining = len(other.FareValidityGapDetails)
		}
		report.FareValidityGapDetails = append(report.FareValidityGapDetails, other.FareValidityGapDetails[:remaining]...)
	}
}
