package tfl

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Sequence struct {
	LineName       string `json:"lineName"`
	Direction      string `json:"direction"`
	IsOutboundOnly bool   `json:"isOutboundOnly"`
	// LineStrings    []string `json:"lineStrings"`
	// Stations
	StopPointSequences []*StopPointSequence `json:"stopPointSequences"`
}
type StopPointSequence struct {
	BranchID   int         `json:"branchId"`
	StopPoints []StopPoint `json:"stopPoint"`
}
type StopPoint struct {
	ID     string `json:"id"`
	Status bool   `json:"status"`
}

func (s *Sequence) Get(lineID string, direction string) error {
	file, err := http.Get(fmt.Sprintf("https://api.tfl.gov.uk/Line/%s/Route/Sequence/%s", lineID, direction))
	if err != nil {
		return err
	}
	defer file.Body.Close()

	err = s.ParseJSON(file.Body)
	if err != nil {
		return err
	}

	return nil
}

func (s *Sequence) ParseJSON(reader io.Reader) error {
	bytes, err := io.ReadAll(reader)

	if err != nil {
		return err
	}

	err = json.Unmarshal(bytes, &s)

	return err
}
