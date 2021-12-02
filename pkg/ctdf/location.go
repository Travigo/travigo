package ctdf

type Location struct {
	Type        string    `json:"-"`
	Coordinates []float64 `json:"coordinates"`
}
