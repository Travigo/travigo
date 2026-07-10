package gtfs

import (
	"math"

	"github.com/travigo/travigo/pkg/ctdf"
)

// maxStopShapeDistanceMetres is deliberately conservative: if a stop does not
// closely match the GTFS shape, retaining the complete journey track is more
// useful than attaching an inaccurate segment to a path item.
const maxStopShapeDistanceMetres = 200

// pathCutLeewayMetres gives adjacent legs a small overlap at each stop. This
// avoids tiny visual and vehicle-matching gaps caused by independent geometry
// rounding at a cut point.
const pathCutLeewayMetres = 5

type trackSnap struct {
	location ctdf.Location
	position float64

	segment  int
	fraction float64
}

type cachedTrackSnap struct {
	snap     trackSnap
	distance float64
	ok       bool
}

// assignJourneyPathTracks splits a journey shape at each stop. A split is only
// applied when every stop can be snapped near the shape in its travel order;
// otherwise Path tracks are left empty and Journey.Track remains the fallback.
func assignJourneyPathTracks(path []*ctdf.JourneyPathItem, stopTimes []StopTime, stopLocations map[string]ctdf.Location, journeyTrack []ctdf.Location) bool {
	return assignJourneyPathTracksCached(path, stopTimes, stopLocations, journeyTrack, nil)
}

func assignJourneyPathTracksCached(path []*ctdf.JourneyPathItem, stopTimes []StopTime, stopLocations map[string]ctdf.Location, journeyTrack []ctdf.Location, snapCache map[string]cachedTrackSnap) bool {
	if len(path) == 0 || len(path) != len(stopTimes)-1 || len(journeyTrack) < 2 {
		return false
	}

	snaps := make([]trackSnap, len(stopTimes))
	for index, stopTime := range stopTimes {
		stopLocation, exists := stopLocations[stopTime.StopID]
		if !exists {
			return false
		}

		cached, exists := snapCache[stopTime.StopID]
		if !exists {
			snap, distance, ok := closestTrackSnap(stopLocation, journeyTrack)
			cached = cachedTrackSnap{snap: snap, distance: distance, ok: ok}
			if snapCache != nil {
				snapCache[stopTime.StopID] = cached
			}
		}
		snap, distance, ok := cached.snap, cached.distance, cached.ok
		if !ok || distance > maxStopShapeDistanceMetres {
			return false
		}
		if index > 0 && snap.position <= snaps[index-1].position {
			return false
		}
		snaps[index] = snap
	}

	legTracks := make([][]ctdf.Location, len(path))
	for index := range path {
		start := offsetTrackSnap(snaps[index], journeyTrack, -pathCutLeewayMetres)
		end := offsetTrackSnap(snaps[index+1], journeyTrack, pathCutLeewayMetres)
		legTracks[index] = trackSlice(start, end, journeyTrack)
		if len(legTracks[index]) < 2 {
			return false
		}
	}

	for index, track := range legTracks {
		path[index].Track = track
	}

	return true
}

func closestTrackSnap(stop ctdf.Location, track []ctdf.Location) (trackSnap, float64, bool) {
	closestDistance := math.Inf(1)
	var closest trackSnap
	found := false

	for index := 0; index < len(track)-1; index++ {
		start, end := track[index], track[index+1]
		if len(start.Coordinates) < 2 || len(end.Coordinates) < 2 {
			continue
		}

		projection, fraction := projectPointOntoSegment(stop, start, end)
		distance := stop.Distance(&projection)
		if distance < closestDistance {
			closestDistance = distance
			closest = trackSnap{
				location: projection,
				position: float64(index) + fraction,
				segment:  index,
				fraction: fraction,
			}
			found = true
		}
	}

	return closest, closestDistance, found
}

func offsetTrackSnap(snap trackSnap, track []ctdf.Location, distanceMetres float64) trackSnap {
	if distanceMetres == 0 {
		return snap
	}

	segment, fraction := snap.segment, snap.fraction
	remaining := math.Abs(distanceMetres)
	for remaining > 0 {
		if distanceMetres > 0 {
			if segment >= len(track)-1 {
				return newTrackSnap(len(track)-2, 1, track)
			}

			segmentLength := track[segment].Distance(&track[segment+1])
			available := segmentLength * (1 - fraction)
			if segmentLength > 0 && remaining <= available {
				return newTrackSnap(segment, fraction+remaining/segmentLength, track)
			}
			remaining -= available
			segment++
			fraction = 0
			continue
		}

		if segment < 0 {
			return newTrackSnap(0, 0, track)
		}

		segmentLength := track[segment].Distance(&track[segment+1])
		available := segmentLength * fraction
		if segmentLength > 0 && remaining <= available {
			return newTrackSnap(segment, fraction-remaining/segmentLength, track)
		}
		remaining -= available
		if segment == 0 {
			return newTrackSnap(0, 0, track)
		}
		segment--
		fraction = 1
	}

	return newTrackSnap(segment, fraction, track)
}

func newTrackSnap(segment int, fraction float64, track []ctdf.Location) trackSnap {
	fraction = math.Max(0, math.Min(1, fraction))
	return trackSnap{
		location: ctdf.Location{
			Type: "Point",
			Coordinates: []float64{
				track[segment].Coordinates[0] + fraction*(track[segment+1].Coordinates[0]-track[segment].Coordinates[0]),
				track[segment].Coordinates[1] + fraction*(track[segment+1].Coordinates[1]-track[segment].Coordinates[1]),
			},
		},
		position: float64(segment) + fraction,
		segment:  segment,
		fraction: fraction,
	}
}

// projectPointOntoSegment uses an equirectangular projection local to the
// segment. It is sufficiently accurate for stop-to-shape snapping while the
// final acceptance check below uses the CTDF haversine distance in metres.
func projectPointOntoSegment(point ctdf.Location, start ctdf.Location, end ctdf.Location) (ctdf.Location, float64) {
	meanLatitudeRadians := ((point.Coordinates[1] + start.Coordinates[1] + end.Coordinates[1]) / 3) * math.Pi / 180
	longitudeScale := math.Cos(meanLatitudeRadians)

	dx := (end.Coordinates[0] - start.Coordinates[0]) * longitudeScale
	dy := end.Coordinates[1] - start.Coordinates[1]
	lengthSquared := dx*dx + dy*dy

	fraction := 0.0
	if lengthSquared > 0 {
		px := (point.Coordinates[0] - start.Coordinates[0]) * longitudeScale
		py := point.Coordinates[1] - start.Coordinates[1]
		fraction = (px*dx + py*dy) / lengthSquared
		fraction = math.Max(0, math.Min(1, fraction))
	}

	return ctdf.Location{
		Type: "Point",
		Coordinates: []float64{
			start.Coordinates[0] + fraction*(end.Coordinates[0]-start.Coordinates[0]),
			start.Coordinates[1] + fraction*(end.Coordinates[1]-start.Coordinates[1]),
		},
	}, fraction
}

func trackSlice(start trackSnap, end trackSnap, track []ctdf.Location) []ctdf.Location {
	if end.position <= start.position {
		return nil
	}

	segmentTrack := make([]ctdf.Location, 0, end.segment-start.segment+3)
	segmentTrack = append(segmentTrack, start.location)
	for index := start.segment + 1; index <= end.segment; index++ {
		position := float64(index)
		if position > start.position && position < end.position {
			segmentTrack = append(segmentTrack, track[index])
		}
	}
	segmentTrack = append(segmentTrack, end.location)

	return segmentTrack
}
