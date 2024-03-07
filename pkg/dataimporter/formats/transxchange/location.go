package transxchange

type Location struct {
	LocationInner

	Translation LocationInner
}
type LocationInner struct {
	Longitude float64
	Latitude  float64

	GridType string
	Easting  string
	Northing string
}
