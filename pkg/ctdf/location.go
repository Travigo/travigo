package ctdf

import "math"

type Location struct {
	Type        string    `json:"point" groups:"basic,detailed,stop-llm"`
	Coordinates []float64 `json:"coordinates" groups:"basic,detailed,stop-llm"`
}

// Shameless taken 'inspiration' from https://stackoverflow.com/a/6853926
func (l *Location) DistanceFromLine(a Location, b Location) float64 {
	A := l.Coordinates[0] - a.Coordinates[0]
	B := l.Coordinates[1] - a.Coordinates[1]
	C := b.Coordinates[0] - a.Coordinates[0]
	D := b.Coordinates[1] - a.Coordinates[1]

	dot := A*C + B*D
	lenSq := C*C + D*D

	var param float64
	param = -1
	if lenSq != 0 {
		param = dot / lenSq
	}

	var xx, yy float64

	if param < 0 {
		xx = a.Coordinates[0]
		yy = a.Coordinates[1]
	} else if param > 1 {
		xx = b.Coordinates[0]
		yy = b.Coordinates[1]
	} else {
		xx = a.Coordinates[0] + param*C
		yy = a.Coordinates[1] + param*D
	}

	var dx = l.Coordinates[0] - xx
	var dy = l.Coordinates[1] - yy
	return math.Sqrt(dx*dx + dy*dy)
}

// Shamelessly stolen from https://gist.github.com/cdipaolo/d3f8db3848278b49db68
func hsin(theta float64) float64 {
	return math.Pow(math.Sin(theta/2), 2)
}
func (l1 *Location) Distance(l2 *Location) float64 {
	// convert to radians
	// must cast radius as float to multiply later
	var la1, lo1, la2, lo2, r float64
	la1 = l1.Coordinates[1] * math.Pi / 180
	lo1 = l1.Coordinates[0] * math.Pi / 180
	la2 = l1.Coordinates[1] * math.Pi / 180
	lo2 = l2.Coordinates[0] * math.Pi / 180

	r = 6378100 // Earth radius in METERS

	// calculate
	h := hsin(la2-la1) + math.Cos(la1)*math.Cos(la2)*hsin(lo2-lo1)

	return 2 * r * math.Asin(math.Sqrt(h))
}
