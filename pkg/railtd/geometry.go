package railtd

import (
	"math"

	"github.com/travigo/travigo/pkg/ctdf"
)

// ValidPoint checks the coordinate range supported by the Redis geo index.
func ValidPoint(point *ctdf.Location) bool {
	return point != nil && point.Type == "Point" && len(point.Coordinates) == 2 &&
		math.Abs(point.Coordinates[0]) <= 180 && math.Abs(point.Coordinates[1]) <= 85.05112878
}

// Project onto the ordered track for validation only. Keep the verified point.
// In Britain 50 m allows for modest alignment offsets and adjacent tracks.
func TrackProgress(point ctdf.Location, track []ctdf.Location) float64 {
	if !ValidPoint(&point) {
		return -1
	}
	best, progress, length := 50.0, -1.0, 0.0
	scale := math.Cos(point.Coordinates[1] * math.Pi / 180)
	for i := 1; i < len(track); i++ {
		a, b := &track[i-1], &track[i]
		if !ValidPoint(a) || !ValidPoint(b) {
			return -1
		}
		dx := (b.Coordinates[0] - a.Coordinates[0]) * scale
		dy := b.Coordinates[1] - a.Coordinates[1]
		if dx == 0 && dy == 0 {
			continue
		}
		fraction := ((point.Coordinates[0]-a.Coordinates[0])*scale*dx + (point.Coordinates[1]-a.Coordinates[1])*dy) / (dx*dx + dy*dy)
		fraction = math.Max(0, math.Min(1, fraction))
		projected := ctdf.Location{Coordinates: []float64{a.Coordinates[0] + fraction*(b.Coordinates[0]-a.Coordinates[0]), a.Coordinates[1] + fraction*dy}}
		distance, segment := point.Distance(&projected), a.Distance(b)
		if distance < best {
			best, progress = distance, length+fraction*segment
		}
		length += segment
	}
	return progress
}
