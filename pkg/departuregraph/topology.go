package departuregraph

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
)

const spatialCellDegrees = 0.01

type stopRecord struct {
	PrimaryRef  stringID
	Longitude   float32
	Latitude    float32
	HasLocation bool
}

type stopIdentifierRecord struct {
	Identifier stringID
	Stop       uint32
}

type spatialCell struct {
	Longitude int16
	Latitude  int16
}

type transferRecord struct {
	ToStop                   uint32
	Type                     uint8
	DistanceMetres           uint32
	WalkDurationSeconds      uint32
	MinChangeDurationSeconds uint32
	TotalDurationSeconds     uint32
}

// Kept as a named slice element so snapshot v5 can grow route/trip-specific
// transfer support without changing the compact unrestricted adjacency record.
type transferRestriction struct {
	Transfer     uint32
	FromRouteRef stringID
	ToRouteRef   stringID
	FromTripRef  stringID
	ToTripRef    stringID
}

type topologyTransfer struct {
	from         uint32
	record       transferRecord
	fromRouteRef string
	toRouteRef   string
	fromTripRef  string
	toTripRef    string
}

func (d *graphData) topologyReady() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.TopologyReady
}

func (d *graphData) loadTopology(ctx context.Context, loader TopologyLoader) error {
	started := time.Now()
	d.mu.Lock()
	d.ensureBuildIndexesLocked()
	d.Stops = nil
	d.StopIdentifiers = nil
	d.StopIndexByStringID = nil
	d.StopGrid = map[spatialCell][]uint32{}
	d.StopAliasOffsets = nil
	d.StopAliases = nil
	d.TransferOffsets = nil
	d.Transfers = nil
	d.TransferRestrictions = nil
	d.IncomingJourneyStateStops = nil
	d.ReverseTransferOffsets = nil
	d.ReverseTransferOrigins = nil
	d.ArrivalPatternOffsets = nil
	d.ArrivalPatterns = nil
	d.StaticPatterns = nil
	d.StaticPatternStops = nil
	d.StaticRoutingReady = false
	d.TopologyReady = false
	d.mu.Unlock()

	identifierToStop := map[string]uint32{}
	stopAliases := make([][]stringID, 0, 65536)
	if err := loader.ScanStops(ctx, func(stop *ctdf.Stop) error {
		if stop == nil || stop.PrimaryIdentifier == "" {
			return nil
		}
		d.mu.Lock()
		index := uint32(len(d.Stops))
		record := stopRecord{PrimaryRef: d.internStop(stop.PrimaryIdentifier)}
		if stop.Location != nil && len(stop.Location.Coordinates) == 2 {
			record.Longitude = float32(stop.Location.Coordinates[0])
			record.Latitude = float32(stop.Location.Coordinates[1])
			record.HasLocation = true
			d.StopGrid[cellForCoordinates(float64(record.Longitude), float64(record.Latitude))] = append(
				d.StopGrid[cellForCoordinates(float64(record.Longitude), float64(record.Latitude))], index,
			)
		}
		d.Stops = append(d.Stops, record)
		aliases := make([]stringID, 0, len(stop.GetAllStopIDs()))
		for _, identifier := range stop.GetAllStopIDs() {
			if identifier == "" {
				continue
			}
			if _, exists := identifierToStop[identifier]; !exists || identifier == stop.PrimaryIdentifier {
				identifierToStop[identifier] = index
			}
			aliases = append(aliases, d.internStop(identifier))
		}
		stopAliases = append(stopAliases, aliases)
		d.mu.Unlock()
		return nil
	}); err != nil {
		return fmt.Errorf("load journey graph stops: %w", err)
	}

	transfers := make([]topologyTransfer, 0, len(d.Stops)*4)
	forbidden := map[[2]uint32]bool{}
	if err := loader.ScanTransfers(ctx, func(transfer *ctdf.StopTransfer) error {
		if transfer == nil || transfer.FromStopRef == "" || transfer.ToStopRef == "" {
			return nil
		}
		from, fromExists := identifierToStop[transfer.FromStopRef]
		to, toExists := identifierToStop[transfer.ToStopRef]
		if !fromExists || !toExists || from == to {
			return nil
		}
		key := [2]uint32{from, to}
		if transfer.Type == ctdf.StopTransferTypeForbidden {
			if transfer.FromRouteRef == "" && transfer.ToRouteRef == "" && transfer.FromTripRef == "" && transfer.ToTripRef == "" {
				forbidden[key] = true
			}
			return nil
		}
		total := transfer.TotalDurationSeconds
		if total <= 0 {
			total = transfer.WalkDurationSeconds + transfer.MinChangeDurationSeconds
		}
		if total <= 0 {
			return nil
		}
		transfers = append(transfers, topologyTransfer{from: from, record: transferRecord{
			ToStop:                   to,
			Type:                     packTransferType(transfer.Type),
			DistanceMetres:           positiveUint32(transfer.DistanceMetres),
			WalkDurationSeconds:      positiveUint32(transfer.WalkDurationSeconds),
			MinChangeDurationSeconds: positiveUint32(transfer.MinChangeDurationSeconds),
			TotalDurationSeconds:     positiveUint32(total),
		}, fromRouteRef: transfer.FromRouteRef, toRouteRef: transfer.ToRouteRef, fromTripRef: transfer.FromTripRef, toTripRef: transfer.ToTripRef})
		return nil
	}); err != nil {
		return fmt.Errorf("load journey graph transfers: %w", err)
	}

	sort.Slice(transfers, func(i, j int) bool {
		if transfers[i].from == transfers[j].from {
			if transfers[i].record.ToStop == transfers[j].record.ToStop {
				if transfers[i].fromRouteRef != transfers[j].fromRouteRef {
					return transfers[i].fromRouteRef < transfers[j].fromRouteRef
				}
				if transfers[i].toRouteRef != transfers[j].toRouteRef {
					return transfers[i].toRouteRef < transfers[j].toRouteRef
				}
				if transfers[i].fromTripRef != transfers[j].fromTripRef {
					return transfers[i].fromTripRef < transfers[j].fromTripRef
				}
				if transfers[i].toTripRef != transfers[j].toTripRef {
					return transfers[i].toTripRef < transfers[j].toTripRef
				}
				if transfers[i].record.Type != transfers[j].record.Type {
					return transfers[i].record.Type < transfers[j].record.Type
				}
				return transfers[i].record.TotalDurationSeconds < transfers[j].record.TotalDurationSeconds
			}
			return transfers[i].record.ToStop < transfers[j].record.ToStop
		}
		return transfers[i].from < transfers[j].from
	})

	d.mu.Lock()
	defer d.mu.Unlock()
	d.StopIdentifiers = make([]stopIdentifierRecord, 0, len(identifierToStop))
	for identifier, stop := range identifierToStop {
		d.StopIdentifiers = append(d.StopIdentifiers, stopIdentifierRecord{Identifier: d.StopIDs[identifier], Stop: stop})
	}
	sort.Slice(d.StopIdentifiers, func(i, j int) bool { return d.StopIdentifiers[i].Identifier < d.StopIdentifiers[j].Identifier })
	d.buildStopIndexByStringIDLocked()
	d.TransferOffsets = make([]uint32, len(d.Stops)+1)
	d.StopAliasOffsets = make([]uint32, len(d.Stops)+1)
	for stopIndex, aliases := range stopAliases {
		d.StopAliasOffsets[stopIndex] = uint32(len(d.StopAliases))
		d.StopAliases = append(d.StopAliases, aliases...)
	}
	d.StopAliasOffsets[len(d.Stops)] = uint32(len(d.StopAliases))
	type transferIdentity struct {
		from, to                             uint32
		kind                                 uint8
		fromRoute, toRoute, fromTrip, toTrip string
	}
	var previous transferIdentity
	previousSet := false
	transferIndex := 0
	for stopIndex := range d.Stops {
		d.TransferOffsets[stopIndex] = uint32(len(d.Transfers))
		for transferIndex < len(transfers) && transfers[transferIndex].from == uint32(stopIndex) {
			transfer := transfers[transferIndex]
			transferIndex++
			key := [2]uint32{transfer.from, transfer.record.ToStop}
			identity := transferIdentity{
				from: transfer.from, to: transfer.record.ToStop, kind: transfer.record.Type,
				fromRoute: transfer.fromRouteRef, toRoute: transfer.toRouteRef,
				fromTrip: transfer.fromTripRef, toTrip: transfer.toTripRef,
			}
			if forbidden[key] || (previousSet && identity == previous) {
				continue
			}
			transferOffset := uint32(len(d.Transfers))
			d.Transfers = append(d.Transfers, transfer.record)
			if transfer.fromRouteRef != "" || transfer.toRouteRef != "" || transfer.fromTripRef != "" || transfer.toTripRef != "" {
				d.TransferRestrictions = append(d.TransferRestrictions, transferRestriction{
					Transfer:     transferOffset,
					FromRouteRef: d.intern(transfer.fromRouteRef), ToRouteRef: d.intern(transfer.toRouteRef),
					FromTripRef: d.intern(transfer.fromTripRef), ToTripRef: d.intern(transfer.toTripRef),
				})
			}
			previous = identity
			previousSet = true
		}
	}
	d.TransferOffsets[len(d.Stops)] = uint32(len(d.Transfers))
	d.rebuildIncomingJourneyStateStopsLocked()
	d.TopologyReady = true
	if len(d.CompleteDays) > 0 {
		d.buildStaticRoutingIndexesLocked()
		d.sealBuildIndexesLocked()
	}
	log.Info().
		Int("stops", len(d.Stops)).
		Int("stop_identifiers", len(d.StopIdentifiers)).
		Int("transfer_edges", len(d.Transfers)).
		Dur("duration", time.Since(started)).
		Msg("Journey graph topology loaded")
	return nil
}

func (d *graphData) rebuildIncomingJourneyStateStopsLocked() {
	d.IncomingJourneyStateStops = make([]bool, len(d.Stops))
	if len(d.TransferOffsets) != len(d.Stops)+1 {
		return
	}
	queue := make([]uint32, 0, len(d.TransferRestrictions))
	stop := 0
	for _, restriction := range d.TransferRestrictions {
		for stop < len(d.Stops) && restriction.Transfer >= d.TransferOffsets[stop+1] {
			stop++
		}
		if stop < len(d.Stops) && restriction.Transfer >= d.TransferOffsets[stop] && !d.IncomingJourneyStateStops[stop] {
			d.IncomingJourneyStateStops[stop] = true
			queue = append(queue, uint32(stop))
		}
	}

	// Same-stop and platform-alias edges preserve the incoming journey. Keep
	// that identity at every stop that can reach a restricted transfer through
	// one of those equivalence edges, not just at the restricted edge itself.
	reverseCounts := make([]uint32, len(d.Stops))
	for origin := range d.Stops {
		for index := d.TransferOffsets[origin]; index < d.TransferOffsets[origin+1]; index++ {
			transfer := d.Transfers[index]
			if int(transfer.ToStop) < len(d.Stops) && isEquivalenceTransfer(unpackTransferType(transfer.Type)) {
				reverseCounts[transfer.ToStop]++
			}
		}
	}
	reverseOffsets := offsetsForCounts(reverseCounts)
	reverseOrigins := make([]uint32, reverseOffsets[len(d.Stops)])
	reverseCursors := append([]uint32(nil), reverseOffsets[:len(d.Stops)]...)
	for origin := range d.Stops {
		for index := d.TransferOffsets[origin]; index < d.TransferOffsets[origin+1]; index++ {
			transfer := d.Transfers[index]
			if int(transfer.ToStop) >= len(d.Stops) || !isEquivalenceTransfer(unpackTransferType(transfer.Type)) {
				continue
			}
			reverseOrigins[reverseCursors[transfer.ToStop]] = uint32(origin)
			reverseCursors[transfer.ToStop]++
		}
	}
	for cursor := 0; cursor < len(queue); cursor++ {
		current := queue[cursor]
		for index := reverseOffsets[current]; index < reverseOffsets[current+1]; index++ {
			origin := reverseOrigins[index]
			if !d.IncomingJourneyStateStops[origin] {
				d.IncomingJourneyStateStops[origin] = true
				queue = append(queue, origin)
			}
		}
	}
}

func (d *graphData) stopIndex(identifier string) (uint32, bool) {
	id, exists := d.StopIDs[identifier]
	if !exists {
		return 0, false
	}
	index := sort.Search(len(d.StopIdentifiers), func(index int) bool {
		return d.StopIdentifiers[index].Identifier >= id
	})
	if index >= len(d.StopIdentifiers) || d.StopIdentifiers[index].Identifier != id {
		return 0, false
	}
	return d.StopIdentifiers[index].Stop, true
}

func (d *graphData) stopIndexForStringID(identifier stringID) (uint32, bool) {
	if int(identifier) >= len(d.StopIndexByStringID) {
		return 0, false
	}
	stop := d.StopIndexByStringID[identifier]
	return stop, stop != math.MaxUint32
}

func (d *graphData) buildStopIndexByStringIDLocked() {
	d.StopIndexByStringID = make([]uint32, len(d.Strings))
	for index := range d.StopIndexByStringID {
		d.StopIndexByStringID[index] = math.MaxUint32
	}
	for _, identifier := range d.StopIdentifiers {
		if int(identifier.Identifier) < len(d.StopIndexByStringID) {
			d.StopIndexByStringID[identifier.Identifier] = identifier.Stop
		}
	}
}

func cellForCoordinates(longitude, latitude float64) spatialCell {
	return spatialCell{
		Longitude: int16(math.Floor(longitude / spatialCellDegrees)),
		Latitude:  int16(math.Floor(latitude / spatialCellDegrees)),
	}
}

func positiveUint32(value int) uint32 {
	if value <= 0 {
		return 0
	}
	return uint32(value)
}

func packTransferType(value ctdf.StopTransferType) uint8 {
	switch value {
	case ctdf.StopTransferTypeSameStopGroup:
		return 2
	case ctdf.StopTransferTypePlatformAlias:
		return 3
	case ctdf.StopTransferTypeRecommended:
		return 4
	case ctdf.StopTransferTypeTimed:
		return 5
	case ctdf.StopTransferTypeMinimumTime:
		return 6
	case ctdf.StopTransferTypeInSeat:
		return 7
	default:
		return 1
	}
}

func unpackTransferType(value uint8) ctdf.StopTransferType {
	switch value {
	case 2:
		return ctdf.StopTransferTypeSameStopGroup
	case 3:
		return ctdf.StopTransferTypePlatformAlias
	case 4:
		return ctdf.StopTransferTypeRecommended
	case 5:
		return ctdf.StopTransferTypeTimed
	case 6:
		return ctdf.StopTransferTypeMinimumTime
	case 7:
		return ctdf.StopTransferTypeInSeat
	default:
		return ctdf.StopTransferTypeNearbyWalk
	}
}
