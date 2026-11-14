package vehicletracker

import (
	"encoding/json"

	"github.com/travigo/travigo/pkg/ctdf"
)

type CacheJourney struct {
	Calls []ctdf.JourneyCall `groups:"detailed"`
}

func (j CacheJourney) MarshalBinary() ([]byte, error) {
	return json.Marshal(j)
}
