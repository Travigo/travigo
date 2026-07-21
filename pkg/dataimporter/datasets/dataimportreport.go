package datasets

import "time"

type DataImportReport struct {
	DatasetIdentifier string

	CreationDateTime time.Time
	RunTime          time.Duration

	ImportedStops           int
	ImportedStopGroups      int
	ImportedServices        int
	ImportedJourneys        int
	ImportedJourneyTracks   int
	ImportedOperators       int
	ImportedOperationGroups int
}
