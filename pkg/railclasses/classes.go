package railclasses

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"

	"github.com/travigo/travigo/data/transforms/railclasses"
	"github.com/travigo/travigo/pkg/ctdf"
	"gopkg.in/yaml.v3"
)

type rule struct {
	Type  string `yaml:"Type"`
	Match struct {
		VehicleType string `yaml:"VehicleType"`
		TrainLength *int   `yaml:"TrainLength"`
	} `yaml:"Match"`
	Data struct {
		VehicleTypeName *string `yaml:"VehicleTypeName"`
		PowerType       *string `yaml:"PowerType"`
		SpeedKMH        *int    `yaml:"SpeedKMH"`
		AirConditioning *bool   `yaml:"AirConditioning"`
		WiFi            *bool   `yaml:"WiFi"`
		Toilets         *bool   `yaml:"Toilets"`
		PowerPlugs      *bool   `yaml:"PowerPlugs"`
		USBPlugs        *bool   `yaml:"USBPlugs"`
		DisabledAccess  *bool   `yaml:"DisabledAccess"`
		BicycleSpaces   *bool   `yaml:"BicycleSpaces"`
		Carriages       []struct {
			ID             string                            `yaml:"ID"`
			Occupancy      int                               `yaml:"Occupancy"`
			SeatingClasses []ctdf.JourneyDetailedRailSeating `yaml:"SeatingClasses"`
			Toilets        []struct {
				Type   string `yaml:"Type"`
				Status string `yaml:"Status"`
			} `yaml:"Toilets"`
		} `yaml:"Carriages"`
	} `yaml:"Data"`
}

var catalogue = sync.OnceValue(func() map[string][]rule {
	rules, err := loadCatalogue(railclassdata.Files)
	if err != nil {
		// The catalogue is embedded in the binary and validated in tests.
		panic(err)
	}
	return rules
})

func loadCatalogue(files fs.FS) (map[string][]rule, error) {
	names, err := fs.Glob(files, "*.yaml")
	if err != nil {
		return nil, err
	}
	rules := make(map[string][]rule)
	for _, name := range names {
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, err
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		for {
			var definition rule
			err := decoder.Decode(&definition)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("decode rail class %s: %w", name, err)
			}
			if definition.Type != "ctdf.RailTrain" || definition.Match.VehicleType == "" {
				return nil, fmt.Errorf("invalid rail class rule in %s", name)
			}
			class := definition.Match.VehicleType
			rules[class] = append(rules[class], definition)
		}
	}
	return rules, nil
}

func set[T any](field *T, value *T) {
	if value != nil {
		*field = *value
	}
}

// Enrich fills class details and applies a matching layout to passenger cars.
func Enrich(train *ctdf.RailTrain) {
	if train.VehicleType == "" {
		return
	}
	var layout []ctdf.RailCarriage
	for _, rule := range catalogue()[train.VehicleType] {
		if rule.Match.TrainLength != nil && *rule.Match.TrainLength != train.TrainLength {
			continue
		}
		data := &rule.Data
		set(&train.VehicleTypeName, data.VehicleTypeName)
		set(&train.PowerType, data.PowerType)
		set(&train.SpeedKMH, data.SpeedKMH)
		set(&train.AirConditioning, data.AirConditioning)
		set(&train.WiFi, data.WiFi)
		set(&train.Toilets, data.Toilets)
		set(&train.PowerPlugs, data.PowerPlugs)
		set(&train.USBPlugs, data.USBPlugs)
		set(&train.DisabledAccess, data.DisabledAccess)
		set(&train.BicycleSpaces, data.BicycleSpaces)
		for _, coach := range data.Carriages {
			carriage := ctdf.RailCarriage{
				ID: coach.ID, Occupancy: coach.Occupancy,
				VehicleRole:    ctdf.RailCarriageVehicleRolePassenger,
				SeatingClasses: append([]ctdf.JourneyDetailedRailSeating(nil), coach.SeatingClasses...),
			}
			for _, toilet := range coach.Toilets {
				carriage.Toilets = append(carriage.Toilets, ctdf.RailCarriageToilet{Type: toilet.Type, Status: toilet.Status})
			}
			layout = append(layout, carriage)
		}
	}

	if len(train.Carriages) == 0 {
		train.Carriages = layout
		return
	}
	index := 0
	for i := range train.Carriages {
		carriage := &train.Carriages[i]
		if !carriage.CountsTowardsTrainLength() {
			continue
		}
		if index == len(layout) {
			break
		}
		if len(carriage.SeatingClasses) == 0 {
			carriage.SeatingClasses = layout[index].SeatingClasses
		}
		if len(carriage.Toilets) == 0 {
			carriage.Toilets = layout[index].Toilets
		}
		index++
	}
}
