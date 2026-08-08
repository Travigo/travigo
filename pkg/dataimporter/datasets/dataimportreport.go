package datasets

import "time"

type DataImportReport struct {
	DatasetIdentifier string

	CreationDateTime time.Time     `groups:"web-import-report"`
	RunTime          time.Duration `groups:"web-import-report"`

	ImportedStops           int `groups:"web-import-report"`
	ImportedStopGroups      int `groups:"web-import-report"`
	ImportedServices        int `groups:"web-import-report"`
	ImportedJourneys        int `groups:"web-import-report"`
	ImportedJourneyTracks   int
	ImportedOperators       int `groups:"web-import-report"`
	ImportedOperationGroups int `groups:"web-import-report"`
}
