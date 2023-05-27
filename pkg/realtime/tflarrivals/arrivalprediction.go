package tflarrivals

type ArrivalPrediction struct {
	ID            string `json:"id"`
	OperationType int    `json:"operationType"`
	VehicleID     string `json:"vehicleId"`

	NaptanID    string `json:"naptanId"`
	StationName string `json:"stationName"`

	LineID   string `json:"lineId"`
	LineName string `json:"lineName"`

	PlatformName string `json:"platformName"`
	Direction    string `json:"direction"`
	Bearing      string `json:"bearing"`

	DestinationNaptanID string `json:"destinationNaptanId"`
	DestinationName     string `json:"destinationName"`

	TimeToStation int `json:"timeToStation"`

	CurrentLocation string `json:"currentLocation"`
	Towards         string `json:"towards"`

	ExpectedArrival string `json:"expectedArrival"`

	ModeName string `json:"modeName"`
}
