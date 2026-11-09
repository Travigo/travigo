package ctdf

import "time"

type DatasetVersion struct {
	Dataset      string
	Hash         string
	ETag         string
	LastModified time.Time

	RawJourneysModified      time.Time `bson:",omitempty"`
	PublishedJourneysVersion time.Time `bson:",omitempty"`
	IndexedStopsVersion      time.Time `bson:",omitempty"`
}
