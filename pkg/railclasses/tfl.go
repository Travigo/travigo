package railclasses

import "github.com/travigo/travigo/pkg/ctdf"

type tubeStock struct {
	vehicleType string
	trainLength int
}

// tubeStocks maps TfL line IDs to the stock normally assigned to that line.
// TfL's vehicleId is retained as the train ID; the arrivals API does not
// provide a stock-class field. Piccadilly remains mapped to 1973 stock until
// the new trains enter passenger service and their TfL set numbers are known.
var tubeStocks = map[string]tubeStock{
	"bakerloo":         {"gb-tfl-stock-1972", 7},
	"piccadilly":       {"gb-tfl-stock-1973", 6},
	"central":          {"gb-tfl-stock-1992-central", 8},
	"waterloo-city":    {"gb-tfl-stock-1992-waterloo-city", 4},
	"northern":         {"gb-tfl-stock-1995", 6},
	"jubilee":          {"gb-tfl-stock-1996", 7},
	"victoria":         {"gb-tfl-stock-2009", 8},
	"district":         {"gb-tfl-stock-s7", 7},
	"circle":           {"gb-tfl-stock-s7", 7},
	"hammersmith-city": {"gb-tfl-stock-s7", 7},
	"metropolitan":     {"gb-tfl-stock-s8", 8},
}

// TfLTubeTrain returns the standard formation for a Tube arrival. It only
// classifies known Underground line IDs and leaves the TfL set number intact.
func TfLTubeTrain(lineID, vehicleID string) (ctdf.RailTrain, bool) {
	stock, ok := tubeStocks[lineID]
	if !ok || vehicleID == "" {
		return ctdf.RailTrain{}, false
	}
	return ctdf.RailTrain{
		ID:          vehicleID,
		VehicleType: stock.vehicleType,
		TrainLength: stock.trainLength,
	}, true
}
