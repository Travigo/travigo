package ctdf

import (
	"encoding/binary"
	"io"
	"math"
	"time"
)

const GBStopIDFormat = "GB:ATCO:%s"

type Stop struct {
	PrimaryIdentifier string   `groups:"basic,search,search-llm,stop-llm" bson:",omitempty"`
	OtherIdentifiers  []string `groups:"basic,search" bson:",omitempty"`

	CreationDateTime     time.Time `groups:"detailed" bson:",omitempty"`
	ModificationDateTime time.Time `groups:"detailed" bson:",omitempty"`

	DataSource *DataSource `groups:"internal" bson:",omitempty"`

	PrimaryName    string          `groups:"basic,search,search-llm,stop-llm" bson:",omitempty"`
	Descriptor     string          `groups:"basic" bson:",omitempty"`
	TransportTypes []TransportType `groups:"detailed,search,search-llm,stop-llm" bson:",omitempty"`

	Timezone string `groups:"basic" bson:",omitempty"`

	Location *Location `groups:"basic,stop-llm" bson:",omitempty"`

	Services []*Service `bson:"-" groups:"basic,search,search-llm,stop-llm" bson:",omitempty"`

	Active bool `groups:"basic" bson:",omitempty"`

	Associations []*Association `groups:"detailed" bson:",omitempty"`

	Platforms []*StopPlatform `groups:"detailed" bson:",omitempty"`
	// Entrances []*StopEntrance `groups:"detailed" bson:",omitempty"`
}

type StopPlatform struct {
	PrimaryIdentifier string `groups:"basic"`

	PrimaryName string `groups:"basic"`

	Location *Location `groups:"detailed"`
}

type StopEntrance struct {
	PrimaryIdentifier string `groups:"basic"`

	PrimaryName string `groups:"basic"`

	Location *Location `groups:"detailed"`
}

func (stop *Stop) GetAllStopIDs() []string {
	allStopIDs := []string{
		stop.PrimaryIdentifier,
	}

	allStopIDs = append(allStopIDs, stop.OtherIdentifiers...)

	return allStopIDs
}

func (stop *Stop) UpdateNameFromServiceOverrides(service *Service) {
	if service == nil {
		return
	}

	for _, stopID := range stop.GetAllStopIDs() {
		if service.StopNameOverrides[stopID] != "" {
			stop.PrimaryName = service.StopNameOverrides[stopID]

			return
		}
	}
}

// Still not perfect as something like st pancras actually covers multiple coordinates
func (stop *Stop) GenerateDeterministicID(writer io.Writer) {
	for _, transportType := range stop.TransportTypes {
		writer.Write([]byte(transportType))
	}

	writer.Write([]byte(stop.Location.Type))

	for _, coord := range stop.Location.Coordinates {
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(coord))
		writer.Write(buf)
	}
}
