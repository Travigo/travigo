package transxchange

import "errors"

type TransXChange struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	Operators              []Operator
	Routes                 []Route
	Services               []Service
	JourneyPatternSections []JourneyPatternSection
	RouteSections          []RouteSection
	VehicleJourneys        []VehicleJourney

	SchemaVersion string `xml:",attr"`
}

func (n *TransXChange) Validate() error {
	if n.CreationDateTime == "" {
		return errors.New("CreationDateTime must be set")
	}
	if n.ModificationDateTime == "" {
		return errors.New("ModificationDateTime must be set")
	}
	if n.SchemaVersion != "2.4" {
		return errors.New("SchemaVersion must be 2.4")
	}

	return nil
}
