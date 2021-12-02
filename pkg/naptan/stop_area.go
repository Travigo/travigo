package naptan

import (
	"fmt"

	"github.com/britbus/britbus/pkg/ctdf"
)

type StopArea struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	StopAreaCode          string
	Name                  string
	AdministrativeAreaRef string
	StopAreaType          string

	Location *Location

	Stops []StopPoint
}

func (orig *StopArea) ToCTDF() *ctdf.StopGroup {
	ctdfStopGroup := ctdf.StopGroup{
		Identifier:           fmt.Sprintf("UK%s", orig.StopAreaCode),
		Name:                 orig.Name,
		Status:               orig.Status,
		CreationDateTime:     orig.CreationDateTime,
		ModificationDateTime: orig.ModificationDateTime,
	}

	switch orig.StopAreaType {
	case "GPBS":
		ctdfStopGroup.Type = "pair"
	case "GCLS":
		ctdfStopGroup.Type = "cluster"
	case "GBCS":
		ctdfStopGroup.Type = "bus_station"
	case "GMLT":
		ctdfStopGroup.Type = "multimode_interchange"
	default:
		ctdfStopGroup.Type = "unknown"
	}

	return &ctdfStopGroup
}
