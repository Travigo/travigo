package ctdf

import (
	"encoding/binary"
	"io"
	"math"
	"time"
)

const GBStopIDFormat = "gb-atco-%s"

type Stop struct {
	PrimaryIdentifier string   `groups:"basic,search,search-llm,stop-llm" bson:",omitempty"`
	OtherIdentifiers  []string `groups:"basic,search" bson:",omitempty"`

	CreationDateTime     time.Time `groups:"detailed" bson:",omitempty"`
	ModificationDateTime time.Time `groups:"detailed" bson:",omitempty"`

	DataSource *DataSourceReference `groups:"detailed" bson:",omitempty"`

	PrimaryName    string          `groups:"basic,search,search-llm,stop-llm" bson:",omitempty"`
	Descriptor     string          `groups:"basic,search" bson:",omitempty"`
	TransportTypes []TransportType `groups:"detailed,search,search-llm,stop-llm" bson:",omitempty"`

	Timezone string `groups:"basic" bson:",omitempty"`

	Location *Location `groups:"basic,stop-llm" bson:",omitempty"`

	Services []*Service `bson:"-" groups:"basic,search,search-llm,stop-llm"`

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

func (stop *Stop) GetPrimaryIdentifier() string {
	return stop.PrimaryIdentifier
}
func (stop *Stop) GetCreationDateTime() time.Time {
	return stop.CreationDateTime
}
func (stop *Stop) SetPrimaryIdentifier(id string) {
	stop.PrimaryIdentifier = id
}
func (stop *Stop) SetOtherIdentifiers(ids []string) {
	stop.OtherIdentifiers = ids
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

	if stop.Location == nil {
		writer.Write([]byte(stop.DataSource.DatasetID))
		writer.Write([]byte(stop.PrimaryIdentifier))
	} else {
		writer.Write([]byte(stop.Location.Type))

		for _, coord := range stop.Location.Coordinates {
			buf := make([]byte, 8)
			binary.BigEndian.PutUint64(buf[:], math.Float64bits(coord))
			writer.Write(buf)
		}
	}
}
