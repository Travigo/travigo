package osmrailtracks

import (
	"bufio"
	"container/heap"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"

	"github.com/qedus/osmpbf"
	"github.com/travigo/travigo/pkg/ctdf"
)

const (
	nodeRecordSize       = 24
	spatialCellSize      = 0.01
	defaultSnapDistanceM = 2500
)

type point struct {
	lon float64
	lat float64
}

type graphEdge struct {
	to       int
	distance float64
}

type graphNode struct {
	point
	edges []graphEdge
}

type railGraph struct {
	nodes   []graphNode
	spatial map[spatialCell][]int
}

type railWay struct {
	nodeIDs        []int64
	costMultiplier float64
}

type indexedRailWay struct {
	nodeIndexes    []int
	costMultiplier float64
}

func parseRailGraph(reader io.Reader) (*railGraph, error) {
	nodeFile, err := os.CreateTemp("", "travigo-osm-rail-nodes-*")
	if err != nil {
		return nil, err
	}
	nodeFileName := nodeFile.Name()
	defer os.Remove(nodeFileName)

	nodeWriter := bufio.NewWriterSize(nodeFile, 1024*1024)
	decoder := osmpbf.NewDecoder(reader)
	decoder.SetBufferSize(osmpbf.MaxBlobSize)
	if err := decoder.Start(max(1, runtime.GOMAXPROCS(0))); err != nil {
		nodeFile.Close()
		return nil, err
	}

	graph := &railGraph{spatial: map[spatialCell][]int{}}
	nodeIndexByID := map[int64]int{}
	ways := make([]indexedRailWay, 0, 100_000)
	nodeIndex := func(nodeID int64) int {
		if index, exists := nodeIndexByID[nodeID]; exists {
			return index
		}
		index := len(graph.nodes)
		nodeIndexByID[nodeID] = index
		graph.nodes = append(graph.nodes, graphNode{})
		return index
	}
	var record [nodeRecordSize]byte
	for {
		value, err := decoder.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			nodeFile.Close()
			return nil, fmt.Errorf("decode OSM PBF: %w", err)
		}

		switch value := value.(type) {
		case *osmpbf.Node:
			binary.LittleEndian.PutUint64(record[0:8], uint64(value.ID))
			binary.LittleEndian.PutUint64(record[8:16], math.Float64bits(value.Lon))
			binary.LittleEndian.PutUint64(record[16:24], math.Float64bits(value.Lat))
			if _, err := nodeWriter.Write(record[:]); err != nil {
				nodeFile.Close()
				return nil, err
			}
		case *osmpbf.Way:
			if !isUsableRailWay(value.Tags) || len(value.NodeIDs) < 2 {
				continue
			}
			nodeIndexes := make([]int, len(value.NodeIDs))
			for index, nodeID := range value.NodeIDs {
				nodeIndexes[index] = nodeIndex(nodeID)
			}
			ways = append(ways, indexedRailWay{nodeIndexes: nodeIndexes, costMultiplier: railWayCostMultiplier(value.Tags)})
		}
	}
	if err := nodeWriter.Flush(); err != nil {
		nodeFile.Close()
		return nil, err
	}
	if _, err := nodeFile.Seek(0, io.SeekStart); err != nil {
		nodeFile.Close()
		return nil, err
	}

	present := make([]bool, len(graph.nodes))
	nodeReader := bufio.NewReaderSize(nodeFile, 1024*1024)
	for {
		_, err := io.ReadFull(nodeReader, record[:])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			nodeFile.Close()
			return nil, err
		}
		nodeID := int64(binary.LittleEndian.Uint64(record[0:8]))
		index, needed := nodeIndexByID[nodeID]
		if !needed {
			continue
		}
		graph.nodes[index].point = point{
			lon: math.Float64frombits(binary.LittleEndian.Uint64(record[8:16])),
			lat: math.Float64frombits(binary.LittleEndian.Uint64(record[16:24])),
		}
		present[index] = true
	}
	if err := nodeFile.Close(); err != nil {
		return nil, err
	}

	for index, found := range present {
		if found {
			cell := cellFor(graph.nodes[index].point)
			graph.spatial[cell] = append(graph.spatial[cell], index)
		}
	}
	// Ways now use compact indexes, so release the large OSM ID lookup before
	// allocating the graph's bidirectional edge lists.
	nodeIndexByID = nil
	runtime.GC()
	addIndexedWays(graph, ways, present)
	if !graphHasEdges(graph) {
		return nil, errors.New("OSM extract contains no connected railway=rail ways")
	}
	return graph, nil
}

func graphHasEdges(graph *railGraph) bool {
	for index := range graph.nodes {
		if len(graph.nodes[index].edges) > 0 {
			return true
		}
	}
	return false
}

func isUsableRailWay(tags map[string]string) bool {
	if tags["railway"] != "rail" {
		return false
	}
	return tags["access"] != "private"
}

func railWayCostMultiplier(tags map[string]string) float64 {
	switch tags["service"] {
	case "yard", "siding", "spur":
		// Keep station throats and terminal platform tracks connected, but do
		// not let routing prefer them as shortcuts over the running lines.
		return 4
	default:
		return 1
	}
}

func buildRailGraph(ways []railWay, coordinates map[int64]point) *railGraph {
	graph := &railGraph{spatial: map[spatialCell][]int{}}
	indexByID := make(map[int64]int, len(coordinates))
	present := []bool{}
	nodeIndex := func(nodeID int64) (int, bool) {
		if index, ok := indexByID[nodeID]; ok {
			return index, true
		}
		location, ok := coordinates[nodeID]
		if !ok {
			return 0, false
		}
		index := len(graph.nodes)
		indexByID[nodeID] = index
		graph.nodes = append(graph.nodes, graphNode{point: location})
		present = append(present, true)
		cell := cellFor(location)
		graph.spatial[cell] = append(graph.spatial[cell], index)
		return index, true
	}

	indexedWays := make([]indexedRailWay, 0, len(ways))
	for _, way := range ways {
		indexes := make([]int, 0, len(way.nodeIDs))
		valid := true
		for _, nodeID := range way.nodeIDs {
			index, ok := nodeIndex(nodeID)
			if !ok {
				valid = false
				break
			}
			indexes = append(indexes, index)
		}
		if valid {
			indexedWays = append(indexedWays, indexedRailWay{nodeIndexes: indexes, costMultiplier: way.costMultiplier})
		}
	}
	addIndexedWays(graph, indexedWays, present)
	return graph
}

func addIndexedWays(graph *railGraph, ways []indexedRailWay, present []bool) {
	for _, way := range ways {
		multiplier := way.costMultiplier
		if multiplier < 1 {
			multiplier = 1
		}
		for i := 1; i < len(way.nodeIndexes); i++ {
			from, to := way.nodeIndexes[i-1], way.nodeIndexes[i]
			if from < 0 || from >= len(present) || to < 0 || to >= len(present) || !present[from] || !present[to] || from == to {
				continue
			}
			distance := distanceMetres(graph.nodes[from].point, graph.nodes[to].point) * multiplier
			if distance <= 0 {
				continue
			}
			graph.nodes[from].edges = append(graph.nodes[from].edges, graphEdge{to: to, distance: distance})
			graph.nodes[to].edges = append(graph.nodes[to].edges, graphEdge{to: from, distance: distance})
		}
	}
}

type spatialCell struct {
	x int
	y int
}

func cellFor(location point) spatialCell {
	return spatialCell{x: int(math.Floor(location.lon / spatialCellSize)), y: int(math.Floor(location.lat / spatialCellSize))}
}

func (graph *railGraph) nearestNode(location point, maximumDistance float64) (int, float64, bool) {
	if maximumDistance <= 0 {
		maximumDistance = defaultSnapDistanceM
	}
	centre := cellFor(location)
	latitudeCellMetres := 111_320 * spatialCellSize
	longitudeCellMetres := latitudeCellMetres * math.Max(0.2, math.Cos(location.lat*math.Pi/180))
	radius := int(math.Ceil(maximumDistance / math.Min(latitudeCellMetres, longitudeCellMetres)))
	bestIndex, bestDistance := -1, maximumDistance
	for x := centre.x - radius; x <= centre.x+radius; x++ {
		for y := centre.y - radius; y <= centre.y+radius; y++ {
			for _, index := range graph.spatial[spatialCell{x: x, y: y}] {
				distance := distanceMetres(location, graph.nodes[index].point)
				if distance <= bestDistance {
					bestIndex, bestDistance = index, distance
				}
			}
		}
	}
	return bestIndex, bestDistance, bestIndex >= 0
}

type routeQueueItem struct {
	node     int
	priority float64
}

type routeQueue []routeQueueItem

func (queue routeQueue) Len() int           { return len(queue) }
func (queue routeQueue) Less(i, j int) bool { return queue[i].priority < queue[j].priority }
func (queue routeQueue) Swap(i, j int)      { queue[i], queue[j] = queue[j], queue[i] }
func (queue *routeQueue) Push(value any)    { *queue = append(*queue, value.(routeQueueItem)) }
func (queue *routeQueue) Pop() any {
	old := *queue
	value := old[len(old)-1]
	*queue = old[:len(old)-1]
	return value
}

func (graph *railGraph) route(from, to int) ([]ctdf.Location, error) {
	if from < 0 || from >= len(graph.nodes) || to < 0 || to >= len(graph.nodes) {
		return nil, errors.New("rail route endpoint is outside graph")
	}
	if from == to {
		return []ctdf.Location{toLocation(graph.nodes[from].point)}, nil
	}

	distances := map[int]float64{from: 0}
	previous := map[int]int{}
	queue := &routeQueue{{node: from, priority: distanceMetres(graph.nodes[from].point, graph.nodes[to].point)}}
	heap.Init(queue)
	for queue.Len() > 0 {
		current := heap.Pop(queue).(routeQueueItem)
		currentDistance, exists := distances[current.node]
		if !exists {
			continue
		}
		expectedPriority := currentDistance + distanceMetres(graph.nodes[current.node].point, graph.nodes[to].point)
		if current.priority > expectedPriority+0.001 {
			continue
		}
		if current.node == to {
			break
		}
		for _, edge := range graph.nodes[current.node].edges {
			candidate := currentDistance + edge.distance
			existing, seen := distances[edge.to]
			if seen && existing <= candidate {
				continue
			}
			distances[edge.to] = candidate
			previous[edge.to] = current.node
			heap.Push(queue, routeQueueItem{
				node:     edge.to,
				priority: candidate + distanceMetres(graph.nodes[edge.to].point, graph.nodes[to].point),
			})
		}
	}
	if _, found := distances[to]; !found {
		return nil, errors.New("rail graph has no path between stops")
	}

	indexes := []int{to}
	for indexes[len(indexes)-1] != from {
		parent, found := previous[indexes[len(indexes)-1]]
		if !found {
			return nil, errors.New("rail route is incomplete")
		}
		indexes = append(indexes, parent)
	}
	track := make([]ctdf.Location, len(indexes))
	for index := range indexes {
		track[len(indexes)-1-index] = toLocation(graph.nodes[indexes[index]].point)
	}
	return simplifyTrack(track, 5), nil
}

func toLocation(value point) ctdf.Location {
	return ctdf.Location{Type: "Point", Coordinates: []float64{value.lon, value.lat}}
}

func distanceMetres(a, b point) float64 {
	const earthRadius = 6_371_000
	lat1, lat2 := a.lat*math.Pi/180, b.lat*math.Pi/180
	dLat := lat2 - lat1
	dLon := (b.lon - a.lon) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadius * math.Asin(math.Sqrt(h))
}

func simplifyTrack(track []ctdf.Location, toleranceMetres float64) []ctdf.Location {
	if len(track) <= 2 {
		return track
	}
	keep := make([]bool, len(track))
	keep[0], keep[len(track)-1] = true, true
	var simplify func(int, int)
	simplify = func(start, end int) {
		startPoint, endPoint := locationPoint(track[start]), locationPoint(track[end])
		maxDistance, maxIndex := 0.0, -1
		for index := start + 1; index < end; index++ {
			distance := pointSegmentDistanceMetres(locationPoint(track[index]), startPoint, endPoint)
			if distance > maxDistance {
				maxDistance, maxIndex = distance, index
			}
		}
		if maxIndex >= 0 && maxDistance > toleranceMetres {
			keep[maxIndex] = true
			simplify(start, maxIndex)
			simplify(maxIndex, end)
		}
	}
	simplify(0, len(track)-1)
	result := make([]ctdf.Location, 0, len(track))
	for index, location := range track {
		if keep[index] {
			result = append(result, location)
		}
	}
	return result
}

func locationPoint(location ctdf.Location) point {
	return point{lon: location.Coordinates[0], lat: location.Coordinates[1]}
}

func pointSegmentDistanceMetres(value, start, end point) float64 {
	scale := math.Cos((start.lat + end.lat + value.lat) / 3 * math.Pi / 180)
	x, y := (value.lon-start.lon)*111_320*scale, (value.lat-start.lat)*111_320
	endX, endY := (end.lon-start.lon)*111_320*scale, (end.lat-start.lat)*111_320
	lengthSquared := endX*endX + endY*endY
	if lengthSquared == 0 {
		return math.Hypot(x, y)
	}
	projection := max(0.0, min(1.0, (x*endX+y*endY)/lengthSquared))
	return math.Hypot(x-projection*endX, y-projection*endY)
}
