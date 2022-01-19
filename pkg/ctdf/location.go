package ctdf

type Location struct {
	Type        string    `json:"-" groups:"basic"`
	Coordinates []float64 `json:"coordinates" groups:"basic"`
}
