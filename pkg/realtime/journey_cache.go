package realtime

import (
	"encoding/json"

	"github.com/britbus/britbus/pkg/ctdf"
)

type CacheJourney struct {
	Path []*ctdf.JourneyPathItem `groups:"detailed"`
}

func (j CacheJourney) MarshalBinary() ([]byte, error) {
	return json.Marshal(j)
}
