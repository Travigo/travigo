package naptan

import (
	"errors"
)

type NaPTAN struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	SchemaVersion string `xml:",attr"`

	StopPoints []StopPoint
	StopAreas  []StopArea
}

func (n *NaPTAN) Validate() error {
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
