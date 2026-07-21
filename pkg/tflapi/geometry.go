package tflapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/travigo/travigo/pkg/ctdf"
)

const (
	MaxStopDistanceMetres = 500
	TrackCutLeewayMetres  = 5
)

type trackSnap struct {
	location ctdf.Location
	position float64
	segment  int
	fraction float64
	distance float64
	previous int
}

func DecodeLineString(encoded string) ([]ctdf.Location, error) {
	var lineStrings [][][]float64
	if err := json.Unmarshal([]byte(encoded), &lineStrings); err != nil {
		return nil, fmt.Errorf("decode encoded line string: %w", err)
	}
	var track []ctdf.Location
	for _, lineString := range lineStrings {
		for _, coordinate := range lineString {
			if len(coordinate) < 2 {
				continue
			}
			location := ctdf.Location{Type: "Point", Coordinates: []float64{coordinate[0], coordinate[1]}}
			if len(track) == 0 || !sameLocation(track[len(track)-1], location) {
				track = append(track, location)
			}
		}
	}
	if len(track) < 2 {
		return nil, errors.New("line string contains fewer than two usable coordinates")
	}
	return track, nil
}

func SplitTrack(stopLocations []ctdf.Location, track []ctdf.Location) ([][]ctdf.Location, error) {
	if len(stopLocations) < 2 || len(track) < 2 {
		return nil, errors.New("route requires at least two stops and two track points")
	}
	snaps, err := orderedSnaps(stopLocations, track)
	if err != nil {
		track = reverseTrack(track)
		snaps, err = orderedSnaps(stopLocations, track)
		if err != nil {
			return nil, err
		}
	}
	legs := make([][]ctdf.Location, len(stopLocations)-1)
	for index := range legs {
		start := offsetSnap(snaps[index], track, -TrackCutLeewayMetres)
		end := offsetSnap(snaps[index+1], track, TrackCutLeewayMetres)
		legs[index] = sliceTrack(start, end, track)
		if len(legs[index]) < 2 {
			return nil, fmt.Errorf("route leg %d has no usable geometry", index)
		}
	}
	return legs, nil
}

func orderedSnaps(stops []ctdf.Location, track []ctdf.Location) ([]trackSnap, error) {
	candidates := make([][]trackSnap, len(stops))
	for stopIndex, stop := range stops {
		for segment := 0; segment < len(track)-1; segment++ {
			projection, fraction := projectPoint(stop, track[segment], track[segment+1])
			distance := stop.Distance(&projection)
			if distance <= MaxStopDistanceMetres {
				candidates[stopIndex] = append(candidates[stopIndex], trackSnap{location: projection, position: float64(segment) + fraction, segment: segment, fraction: fraction, distance: distance, previous: -1})
			}
		}
		if len(candidates[stopIndex]) == 0 {
			return nil, fmt.Errorf("stop %d is more than %dm from the route track", stopIndex, MaxStopDistanceMetres)
		}
	}
	costs := make([][]float64, len(candidates))
	for i := range candidates {
		costs[i] = make([]float64, len(candidates[i]))
		for j := range costs[i] {
			costs[i][j] = math.Inf(1)
		}
	}
	for i, candidate := range candidates[0] {
		costs[0][i] = candidate.distance
	}
	for stopIndex := 1; stopIndex < len(candidates); stopIndex++ {
		for candidateIndex := range candidates[stopIndex] {
			candidate := &candidates[stopIndex][candidateIndex]
			for previousIndex, previous := range candidates[stopIndex-1] {
				if candidate.position <= previous.position || math.IsInf(costs[stopIndex-1][previousIndex], 1) {
					continue
				}
				cost := costs[stopIndex-1][previousIndex] + candidate.distance
				if cost < costs[stopIndex][candidateIndex] {
					costs[stopIndex][candidateIndex], candidate.previous = cost, previousIndex
				}
			}
		}
	}
	last := len(candidates) - 1
	bestIndex, bestCost := -1, math.Inf(1)
	for index, cost := range costs[last] {
		if cost < bestCost {
			bestIndex, bestCost = index, cost
		}
	}
	if bestIndex < 0 {
		return nil, errors.New("stops cannot be placed on the route track in travel order")
	}
	snaps := make([]trackSnap, len(stops))
	for stopIndex := last; stopIndex >= 0; stopIndex-- {
		snaps[stopIndex] = candidates[stopIndex][bestIndex]
		bestIndex = candidates[stopIndex][bestIndex].previous
	}
	return snaps, nil
}

func projectPoint(point, start, end ctdf.Location) (ctdf.Location, float64) {
	longitudeScale := math.Cos(((point.Coordinates[1] + start.Coordinates[1] + end.Coordinates[1]) / 3) * math.Pi / 180)
	dx, dy := (end.Coordinates[0]-start.Coordinates[0])*longitudeScale, end.Coordinates[1]-start.Coordinates[1]
	fraction, lengthSquared := 0.0, dx*dx+dy*dy
	if lengthSquared > 0 {
		fraction = (((point.Coordinates[0]-start.Coordinates[0])*longitudeScale)*dx + (point.Coordinates[1]-start.Coordinates[1])*dy) / lengthSquared
		fraction = math.Max(0, math.Min(1, fraction))
	}
	return ctdf.Location{Type: "Point", Coordinates: []float64{start.Coordinates[0] + fraction*(end.Coordinates[0]-start.Coordinates[0]), start.Coordinates[1] + fraction*(end.Coordinates[1]-start.Coordinates[1])}}, fraction
}

func offsetSnap(snap trackSnap, track []ctdf.Location, metres float64) trackSnap {
	segment, fraction, remaining := snap.segment, snap.fraction, math.Abs(metres)
	for remaining > 0 {
		length := track[segment].Distance(&track[segment+1])
		if metres > 0 {
			available := length * (1 - fraction)
			if length > 0 && remaining <= available {
				return newSnap(segment, fraction+remaining/length, track)
			}
			remaining -= available
			if segment >= len(track)-2 {
				return newSnap(len(track)-2, 1, track)
			}
			segment, fraction = segment+1, 0
		} else {
			available := length * fraction
			if length > 0 && remaining <= available {
				return newSnap(segment, fraction-remaining/length, track)
			}
			remaining -= available
			if segment == 0 {
				return newSnap(0, 0, track)
			}
			segment, fraction = segment-1, 1
		}
	}
	return newSnap(segment, fraction, track)
}

func newSnap(segment int, fraction float64, track []ctdf.Location) trackSnap {
	fraction = math.Max(0, math.Min(1, fraction))
	return trackSnap{location: ctdf.Location{Type: "Point", Coordinates: []float64{track[segment].Coordinates[0] + fraction*(track[segment+1].Coordinates[0]-track[segment].Coordinates[0]), track[segment].Coordinates[1] + fraction*(track[segment+1].Coordinates[1]-track[segment].Coordinates[1])}}, position: float64(segment) + fraction, segment: segment, fraction: fraction}
}

func sliceTrack(start, end trackSnap, track []ctdf.Location) []ctdf.Location {
	if end.position <= start.position {
		return nil
	}
	leg := []ctdf.Location{start.location}
	for vertex := start.segment + 1; vertex <= end.segment && vertex < len(track); vertex++ {
		if !sameLocation(leg[len(leg)-1], track[vertex]) {
			leg = append(leg, track[vertex])
		}
	}
	if !sameLocation(leg[len(leg)-1], end.location) {
		leg = append(leg, end.location)
	}
	return leg
}

func reverseTrack(track []ctdf.Location) []ctdf.Location {
	reversed := make([]ctdf.Location, len(track))
	for index := range track {
		reversed[len(track)-1-index] = track[index]
	}
	return reversed
}

func sameLocation(a, b ctdf.Location) bool {
	return len(a.Coordinates) >= 2 && len(b.Coordinates) >= 2 && a.Coordinates[0] == b.Coordinates[0] && a.Coordinates[1] == b.Coordinates[1]
}
