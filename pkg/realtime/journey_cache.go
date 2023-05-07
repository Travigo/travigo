package realtime

import (
	"encoding/json"

	"github.com/travigo/travigo/pkg/ctdf"
)

type CacheJourney struct {
	Path []*ctdf.JourneyPathItem `groups:"detailed"`
}

func (j CacheJourney) MarshalBinary() ([]byte, error) {
	return json.Marshal(j)
}
