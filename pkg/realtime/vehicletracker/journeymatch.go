package vehicletracker

import (
	"math"

	"github.com/travigo/travigo/pkg/ctdf"
)

type journeyPositionMatch struct {
	PathIndex       int
	LegProgress     float64
	JourneyProgress float64
	DistanceMetres  float64
	UsedGlobalTrack bool
}

func matchJourneyPosition(journey *ctdf.Journey, vehicle ctdf.Location) (journeyPositionMatch, bool) {
	if journey == nil || vehicle.Type != "Point" || len(vehicle.Coordinates) < 2 {
		return journeyPositionMatch{}, false
	}

	var best journeyPositionMatch
	found := false
	for pathIndex, path := range journey.Path {
		if path == nil || len(path.Track) < 2 {
			continue
		}
		distance, progress, ok := closestTrackPosition(vehicle, path.Track)
		if !ok || (found && distance >= best.DistanceMetres) {
			continue
		}
		best = journeyPositionMatch{PathIndex: pathIndex, LegProgress: progress, DistanceMetres: distance}
		found = true
	}
	if found {
		best.JourneyProgress = (float64(best.PathIndex) + best.LegProgress) / float64(len(journey.Path))
		return best, true
	}

	if len(journey.Track) < 2 || len(journey.Path) == 0 {
		return journeyPositionMatch{}, false
	}
	distance, globalProgress, ok := closestTrackPosition(vehicle, journey.Track)
	if !ok {
		return journeyPositionMatch{}, false
	}
	pathIndex, legProgress := pathForGlobalProgress(journey, globalProgress)
	return journeyPositionMatch{PathIndex: pathIndex, LegProgress: legProgress, JourneyProgress: globalProgress, DistanceMetres: distance, UsedGlobalTrack: true}, true
}

func pathForGlobalProgress(journey *ctdf.Journey, globalProgress float64) (int, float64) {
	previous := 0.0
	for index, path := range journey.Path {
		if path == nil || path.DestinationStop == nil || path.DestinationStop.Location == nil {
			continue
		}
		_, destinationProgress, ok := closestTrackPosition(*path.DestinationStop.Location, journey.Track)
		if !ok {
			continue
		}
		if globalProgress <= destinationProgress || index == len(journey.Path)-1 {
			span := destinationProgress - previous
			if span <= 0 {
				return index, 0
			}
			return index, math.Max(0, math.Min(1, (globalProgress-previous)/span))
		}
		previous = destinationProgress
	}
	return len(journey.Path) - 1, 1
}

func closestTrackPosition(location ctdf.Location, track []ctdf.Location) (float64, float64, bool) {
	total := trackLengthMetres(track)
	if total <= 0 {
		return 0, 0, false
	}
	closestDistance := math.Inf(1)
	closestAlong := 0.0
	along := 0.0
	for index := 0; index < len(track)-1; index++ {
		start, end := track[index], track[index+1]
		if len(start.Coordinates) < 2 || len(end.Coordinates) < 2 {
			continue
		}
		projected, fraction := projectOntoTrackSegment(location, start, end)
		distance := location.Distance(&projected)
		segmentLength := start.Distance(&end)
		if distance < closestDistance {
			closestDistance = distance
			closestAlong = along + fraction*segmentLength
		}
		along += segmentLength
	}
	if math.IsInf(closestDistance, 1) {
		return 0, 0, false
	}
	return closestDistance, closestAlong / total, true
}

func trackLengthMetres(track []ctdf.Location) float64 {
	total := 0.0
	for index := 0; index < len(track)-1; index++ {
		if len(track[index].Coordinates) >= 2 && len(track[index+1].Coordinates) >= 2 {
			total += track[index].Distance(&track[index+1])
		}
	}
	return total
}

func projectOntoTrackSegment(point, start, end ctdf.Location) (ctdf.Location, float64) {
	meanLatitudeRadians := ((point.Coordinates[1] + start.Coordinates[1] + end.Coordinates[1]) / 3) * math.Pi / 180
	longitudeScale := math.Cos(meanLatitudeRadians)
	dx := (end.Coordinates[0] - start.Coordinates[0]) * longitudeScale
	dy := end.Coordinates[1] - start.Coordinates[1]
	lengthSquared := dx*dx + dy*dy
	fraction := 0.0
	if lengthSquared > 0 {
		px := (point.Coordinates[0] - start.Coordinates[0]) * longitudeScale
		py := point.Coordinates[1] - start.Coordinates[1]
		fraction = math.Max(0, math.Min(1, (px*dx+py*dy)/lengthSquared))
	}
	return ctdf.Location{Type: "Point", Coordinates: []float64{start.Coordinates[0] + fraction*(end.Coordinates[0]-start.Coordinates[0]), start.Coordinates[1] + fraction*(end.Coordinates[1]-start.Coordinates[1])}}, fraction
}
