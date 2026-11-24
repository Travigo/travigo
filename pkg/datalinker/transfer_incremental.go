package datalinker

import (
	"math"
	"sort"

	"github.com/travigo/travigo/pkg/ctdf"
)

// transferSnapshot contains only fields that can change generated transfers.
// Slices are canonicalized by snapshotForStop so BSON bytes are stable.
type transferSnapshot struct {
	ID           string   `bson:"_id"`
	HasService   bool     `bson:"service"`
	Latitude     float64  `bson:"lat"`
	Longitude    float64  `bson:"lon"`
	Tile         string   `bson:"tile"`
	Associations []string `bson:"associations"`
	Platforms    []string `bson:"platforms"`
}

func (s transferSnapshot) stop(index int) *transferStop {
	stop := &transferStop{PrimaryIdentifier: s.ID, Index: index, HasService: s.HasService, Latitude: s.Latitude, Longitude: s.Longitude, CosLatitude: math.Cos(s.Latitude * math.Pi / 180)}
	stop.Location = &ctdf.Location{Type: "Point", Coordinates: []float64{s.Longitude, s.Latitude}}
	for _, id := range s.Associations {
		stop.Associations = append(stop.Associations, &ctdf.Association{Type: "stop_group", AssociatedIdentifier: id})
	}
	for _, id := range s.Platforms {
		stop.Platforms = append(stop.Platforms, &ctdf.StopPlatform{PrimaryIdentifier: id})
	}
	return stop
}

func snapshotForStop(stop *transferStop) transferSnapshot {
	if stop == nil {
		return transferSnapshot{}
	}
	s := transferSnapshot{ID: stop.PrimaryIdentifier, HasService: stop.HasService, Latitude: stop.Latitude, Longitude: stop.Longitude, Tile: transferSnapshotTile(stop.Latitude, stop.Longitude)}
	for _, a := range stop.Associations {
		if a != nil && a.AssociatedIdentifier != "" && (a.Type == "" || a.Type == "stop_group") {
			s.Associations = append(s.Associations, a.AssociatedIdentifier)
		}
	}
	for _, p := range stop.Platforms {
		if p != nil && p.PrimaryIdentifier != "" {
			s.Platforms = append(s.Platforms, p.PrimaryIdentifier)
		}
	}
	sort.Strings(s.Associations)
	s.Associations = compactStrings(s.Associations)
	sort.Strings(s.Platforms)
	s.Platforms = compactStrings(s.Platforms)
	return s
}

func compactStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

type transferIndex struct {
	stops            []*transferStop
	byID             map[string]*transferStop
	config           StopTransferBuildConfig
	groups           map[string][]*transferStop
	reversePlatforms map[string][]*transferStop
	grid             map[gridKey][]*transferStop
	cell             float64
}

func newTransferIndex(stops []*transferStop, config StopTransferBuildConfig) *transferIndex {
	config = config.withDefaults()
	idx := &transferIndex{stops: stops, byID: make(map[string]*transferStop, len(stops)), config: config, groups: map[string][]*transferStop{}, reversePlatforms: map[string][]*transferStop{}, grid: map[gridKey][]*transferStop{}, cell: float64(config.MaxDistanceMetres) / metresPerDegree}
	for _, s := range stops {
		if s != nil && s.PrimaryIdentifier != "" {
			idx.byID[s.PrimaryIdentifier] = s
		}
		if s == nil {
			continue
		}
		for _, a := range s.Associations {
			if a != nil && a.AssociatedIdentifier != "" && (a.Type == "" || a.Type == "stop_group") {
				idx.groups[a.AssociatedIdentifier] = append(idx.groups[a.AssociatedIdentifier], s)
			}
		}
		for _, p := range s.Platforms {
			if p != nil && p.PrimaryIdentifier != "" {
				idx.reversePlatforms[p.PrimaryIdentifier] = append(idx.reversePlatforms[p.PrimaryIdentifier], s)
			}
		}
		if s.HasService && idx.cell > 0 {
			k := stopGridKey(s, idx.cell)
			idx.grid[k] = append(idx.grid[k], s)
		}
	}
	return idx
}

// affected returns every current stop whose output may be influenced by a
// changed record, plus removed identifiers so their old edges can be deleted.
func (idx *transferIndex) affected(old map[string]transferSnapshot, changed map[string]bool) map[string]bool {
	out := make(map[string]bool)
	ids := make([]string, 0, len(changed))
	for id, yes := range changed {
		if yes {
			ids = append(ids, id)
			out[id] = true
		}
	}
	for _, id := range ids {
		oldStop, hadOld := old[id]
		newStop := idx.byID[id]
		candidates := map[string]*transferStop{}
		if newStop != nil {
			idx.addRelated(candidates, newStop)
			idx.addSpatial(candidates, newStop.Latitude, newStop.Longitude)
		}
		if hadOld {
			for _, g := range oldStop.Associations {
				for _, s := range idx.groups[g] {
					candidates[s.PrimaryIdentifier] = s
				}
			}
			for _, p := range oldStop.Platforms {
				if s := idx.byID[p]; s != nil {
					candidates[p] = s
				}
				for _, s := range idx.reversePlatforms[p] {
					candidates[s.PrimaryIdentifier] = s
				}
			}
			idx.addSpatial(candidates, oldStop.Latitude, oldStop.Longitude)
		}
		for _, other := range candidates {
			if other.PrimaryIdentifier != id && (newStop != nil && idx.mayInteract(newStop, other) || hadOld && snapshotMayInteract(oldStop, other, idx.config)) {
				out[other.PrimaryIdentifier] = true
			}
		}
		// Follow both directions of platform references and same-group links.
		for _, other := range idx.reversePlatforms[id] {
			out[other.PrimaryIdentifier] = true
		}
	}
	return out
}

func (idx *transferIndex) addRelated(dst map[string]*transferStop, s *transferStop) {
	for _, a := range s.Associations {
		if a != nil {
			for _, m := range idx.groups[a.AssociatedIdentifier] {
				dst[m.PrimaryIdentifier] = m
			}
		}
	}
	for _, p := range s.Platforms {
		if p != nil {
			if m := idx.byID[p.PrimaryIdentifier]; m != nil {
				dst[m.PrimaryIdentifier] = m
			}
			for _, m := range idx.reversePlatforms[p.PrimaryIdentifier] {
				dst[m.PrimaryIdentifier] = m
			}
		}
	}
}
func (idx *transferIndex) addSpatial(dst map[string]*transferStop, lat, lon float64) {
	if idx.cell <= 0 {
		return
	}
	k := gridKey{int(math.Floor(lat / idx.cell)), int(math.Floor(lon / idx.cell))}
	// A neighboring stop's latitude cosine can make the distance calculation
	// directional. Search with the minimum supported cosine to cover both ways.
	maxLat := math.Min(90, math.Abs(lat)+idx.cell)
	cosBound := math.Cos(maxLat * math.Pi / 180)
	if cosBound < .01 {
		cosBound = .01
	}
	xr := int(math.Ceil(float64(idx.config.MaxDistanceMetres) / (metresPerDegree * cosBound) / idx.cell))
	if xr < 1 {
		xr = 1
	}
	for y := -1; y <= 1; y++ {
		for x := -xr; x <= xr; x++ {
			for _, s := range idx.grid[gridKey{k.lat + y, k.lon + x}] {
				dst[s.PrimaryIdentifier] = s
			}
		}
	}
}

func (idx *transferIndex) mayInteract(a, b *transferStop) bool {
	if a == nil || b == nil {
		return false
	}
	if sharedStopGroup(a, b) || stopLinks(a, b.PrimaryIdentifier) || stopLinks(b, a.PrimaryIdentifier) {
		return true
	}
	if !a.HasService || !b.HasService || !validTransferLocation(a.Location) || !validTransferLocation(b.Location) {
		return false
	}
	limit := float64(idx.config.MaxDistanceMetres) * float64(idx.config.MaxDistanceMetres)
	return squaredStopDistanceMetres(a, b) <= limit || squaredStopDistanceMetres(b, a) <= limit
}

func snapshotMayInteract(s transferSnapshot, other *transferStop, config StopTransferBuildConfig) bool {
	if snapshotLinks(s, other.PrimaryIdentifier) || sharedSnapshotGroup(s, other) {
		return true
	}
	if !s.HasService || !other.HasService || !validTransferLocation(other.Location) {
		return false
	}
	a := &transferStop{Latitude: s.Latitude, Longitude: s.Longitude, CosLatitude: math.Cos(s.Latitude * math.Pi / 180)}
	limit := float64(config.MaxDistanceMetres) * float64(config.MaxDistanceMetres)
	return squaredStopDistanceMetres(a, other) <= limit || squaredStopDistanceMetres(other, a) <= limit
}

func (idx *transferIndex) edges(origins []string) map[transferKey]transferCandidate {
	merged := make(map[transferKey]transferCandidate)
	workers := idx.config.WorkerCount
	if workers < 1 {
		workers = 1
	}
	if workers > len(origins) {
		workers = len(origins)
	}
	if workers <= 1 {
		return idx.edgesForOrigins(origins)
	}
	chunk := (len(origins) + workers - 1) / workers
	results := make(chan map[transferKey]transferCandidate, workers)
	for start := 0; start < len(origins); start += chunk {
		end := start + chunk
		if end > len(origins) {
			end = len(origins)
		}
		part := origins[start:end]
		go func() { results <- idx.edgesForOrigins(part) }()
	}
	for i := 0; i < (len(origins)+chunk-1)/chunk; i++ {
		for k, v := range <-results {
			merged[k] = v
		}
	}
	return merged
}

func (idx *transferIndex) edgesForOrigins(origins []string) map[transferKey]transferCandidate {
	result := make(map[transferKey]transferCandidate)
	config := idx.config
	wanted := make(map[string]bool, len(origins))
	for _, id := range origins {
		wanted[id] = true
	}
	// Reproduce group generation, including its per-group source ordering.
	groupLimit := config.MaxDistanceMetres * config.SameStopGroupMultiplier
	if groupLimit < 1000 {
		groupLimit = 1000
	}
	for _, id := range origins {
		a := idx.byID[id]
		if a == nil {
			continue
		}
		seen := map[string]bool{}
		for _, assoc := range a.Associations {
			if assoc == nil {
				continue
			}
			for _, candidate := range idx.groups[assoc.AssociatedIdentifier] {
				b := candidate
				if b == a || seen[b.PrimaryIdentifier] {
					continue
				}
				seen[b.PrimaryIdentifier] = true
				from, to := a, b
				if to.PrimaryIdentifier < from.PrimaryIdentifier {
					from, to = to, from
				}
				d2 := squaredStopDistanceMetres(from, to)
				if d2 >= math.Pow(float64(groupLimit)+.5, 2) {
					continue
				}
				d := int(math.Round(math.Sqrt(d2)))
				addTransfer(result, a.PrimaryIdentifier, b.PrimaryIdentifier, ctdf.StopTransferTypeSameStopGroup, d, groupLimit, config)
			}
		}
	}
	for _, id := range origins {
		s := idx.byID[id]
		if s == nil {
			continue
		}
		for _, p := range s.Platforms {
			if p == nil || p.PrimaryIdentifier == "" {
				continue
			}
			target := idx.byID[p.PrimaryIdentifier]
			if target == nil || !validTransferLocation(target.Location) {
				continue
			}
			d := roundedStopDistanceMetres(s, target)
			addTransfer(result, s.PrimaryIdentifier, target.PrimaryIdentifier, ctdf.StopTransferTypePlatformAlias, d, config.MaxDistanceMetres, config)
		}
		// Child platform references may be the only changed records in a batch.
		for _, parent := range idx.reversePlatforms[id] {
			d := roundedStopDistanceMetres(parent, s)
			addTransfer(result, s.PrimaryIdentifier, parent.PrimaryIdentifier, ctdf.StopTransferTypePlatformAlias, d, config.MaxDistanceMetres, config)
		}
	}
	// Preserve the full collection order and indices used by the full builder,
	// while discovering only requested origins.
	cell := float64(config.MaxDistanceMetres) / metresPerDegree
	if cell > 0 {
		grid := idx.grid
		latRange := int(math.Ceil((float64(config.MaxDistanceMetres) / metresPerDegree) / cell))
		if latRange < 1 {
			latRange = 1
		}
		maxD2 := float64(config.MaxDistanceMetres) * float64(config.MaxDistanceMetres)
		for _, id := range origins {
			from := idx.byID[id]
			if from == nil || !from.HasService {
				continue
			}
			candidates := []nearbyTransferCandidate{}
			fk := stopGridKey(from, cell)
			cos := from.CosLatitude
			if math.Abs(cos) < .01 {
				cos = .01
			}
			lonRange := int(math.Ceil((float64(config.MaxDistanceMetres) / (metresPerDegree * math.Abs(cos))) / cell))
			if lonRange < 1 {
				lonRange = 1
			}
			for dy := -latRange; dy <= latRange; dy++ {
				for dx := -lonRange; dx <= lonRange; dx++ {
					for _, to := range grid[gridKey{lat: fk.lat + dy, lon: fk.lon + dx}] {
						if from.PrimaryIdentifier == to.PrimaryIdentifier || transferExists(result, from.PrimaryIdentifier, to.PrimaryIdentifier) {
							continue
						}
						d2 := squaredStopDistanceMetres(from, to)
						if d2 > maxD2 {
							continue
						}
						addNearbyCandidate(&candidates, to.Index, idx.stops, int(math.Round(math.Sqrt(d2))), config.MaxNearbyTransfersPerStop)
					}
				}
			}
			for _, c := range candidates {
				addTransfer(result, from.PrimaryIdentifier, idx.stops[c.toStopIndex].PrimaryIdentifier, ctdf.StopTransferTypeNearbyWalk, c.distanceMetres, config.MaxDistanceMetres, config)
			}
		}
	}
	for key := range result {
		if !wanted[key.from] {
			delete(result, key)
		}
	}
	return result
}

func sharedStopGroup(a, b *transferStop) bool {
	for _, x := range a.Associations {
		if x != nil && (x.Type == "" || x.Type == "stop_group") {
			for _, y := range b.Associations {
				if y != nil && x.AssociatedIdentifier != "" && x.AssociatedIdentifier == y.AssociatedIdentifier && (y.Type == "" || y.Type == "stop_group") {
					return true
				}
			}
		}
	}
	return false
}
func stopLinks(s *transferStop, id string) bool {
	for _, p := range s.Platforms {
		if p != nil && p.PrimaryIdentifier == id {
			return true
		}
	}
	return false
}
func snapshotLinks(s transferSnapshot, id string) bool {
	for _, v := range s.Platforms {
		if v == id {
			return true
		}
	}
	return false
}
func sharedSnapshotGroup(s transferSnapshot, other *transferStop) bool {
	for _, x := range s.Associations {
		for _, a := range other.Associations {
			if a != nil && a.AssociatedIdentifier == x && (a.Type == "" || a.Type == "stop_group") {
				return true
			}
		}
	}
	return false
}
