package ctdf

import "math"

type Location struct {
	Type        string    `json:"-" groups:"basic"`
	Coordinates []float64 `json:"coordinates" groups:"basic"`
}

// Shameless taken 'inspiration' from https://stackoverflow.com/a/6853926
func (l *Location) DistanceFromLine(a Location, b Location) float64 {
	A := l.Coordinates[0] - a.Coordinates[0]
	B := l.Coordinates[1] - a.Coordinates[1]
	C := b.Coordinates[0] - a.Coordinates[0]
	D := b.Coordinates[1] - a.Coordinates[1]

	dot := A*C + B*D
	len_sq := C*C + D*D

	var param float64
	param = -1
	if len_sq != 0 {
		param = dot / len_sq
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
