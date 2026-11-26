package tfl

import (
	"context"
	"slices"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type boardDirectionPath struct {
	before, after []string // nearest call first on either side of the board stop
}

func (p boardDirectionPath) empty() bool {
	return len(p.before) == 0 && len(p.after) == 0
}

func liveBoardDirectionPath(journey *ctdf.RealtimeJourney, prediction *ctdf.RealtimeJourneyStops, stopIDs map[string]struct{}) boardDirectionPath {
	calls := journey.Journey.Calls
	boardIndex := -1
	for i, call := range calls {
		if _, matches := stopIDs[call.StopRef]; !matches {
			continue
		}
		if boardIndex >= 0 {
			boardIndex = -1
			break
		}
		boardIndex = i
	}
	if boardIndex >= 0 {
		return directionPathAt(calls, boardIndex, stopIDs)
	}

	// The tracker indexes observed calls. A reconstructed route can include
	// extra calls, so use observations to anchor repeated or missing visits.
	stops := make([]*ctdf.RealtimeJourneyStops, 0, len(journey.Stops))
	for _, stop := range journey.Stops {
		if stop != nil {
			stops = append(stops, stop)
		}
	}
	slices.SortFunc(stops, func(a, b *ctdf.RealtimeJourneyStops) int {
		return a.JourneyStopIndex - b.JourneyStopIndex
	})
	observedCalls := make([]ctdf.JourneyCall, len(stops))
	for index, stop := range stops {
		observedCalls[index].StopRef = stop.StopRef
		if stop == prediction {
			boardIndex = index
		}
	}
	return directionPathAt(observedCalls, boardIndex, stopIDs)
}

func scheduledBoardDirectionPath(entry *ctdf.DepartureBoard, stopIDs map[string]struct{}, boardType ctdf.BoardType) boardDirectionPath {
	calls := entry.Journey.Calls
	boardIndex, firstStopIndex, occurrenceIndex := -1, 0, 0
	multiple := false
	for i := 0; i+1 < len(calls); i++ {
		if calls[i].End || calls[i+1].Start {
			continue
		}
		callIndex, stopIndex := i, occurrenceIndex
		occurrenceIndex++
		if boardType.IsArrival() {
			callIndex++
			stopIndex++
		}
		if _, matches := stopIDs[calls[callIndex].StopRef]; !matches {
			continue
		}
		if boardIndex < 0 {
			boardIndex, firstStopIndex = callIndex, stopIndex
			continue
		}
		if entry.OccurrenceIdentifier == "" {
			return boardDirectionPath{}
		}
		if !multiple && entry.OccurrenceIdentifier == ctdf.BoardOccurrenceIdentifier(
			entry.Journey.PrimaryIdentifier, entry.ServiceDate, firstStopIndex, boardType,
		) {
			return directionPathAt(calls, boardIndex, stopIDs)
		}
		if entry.OccurrenceIdentifier == ctdf.BoardOccurrenceIdentifier(
			entry.Journey.PrimaryIdentifier, entry.ServiceDate, stopIndex, boardType,
		) {
			return directionPathAt(calls, callIndex, stopIDs)
		}
		multiple = true
	}
	if multiple {
		return boardDirectionPath{}
	}
	return directionPathAt(calls, boardIndex, stopIDs)
}

func directionPathAt(calls []ctdf.JourneyCall, boardIndex int, stopIDs map[string]struct{}) boardDirectionPath {
	var path boardDirectionPath
	if boardIndex < 0 {
		return path
	}
	for i := boardIndex - 1; i >= 0 && !calls[i].End && !calls[i+1].Start; i-- {
		stopRef := calls[i].StopRef
		if _, atBoard := stopIDs[stopRef]; atBoard {
			break
		}
		path.before = append(path.before, stopRef)
	}
	for i := boardIndex + 1; i < len(calls) && !calls[i-1].End && !calls[i].Start; i++ {
		stopRef := calls[i].StopRef
		if _, atBoard := stopIDs[stopRef]; atBoard {
			break
		}
		path.after = append(path.after, stopRef)
	}
	return path
}

type timedDirectionPath struct {
	time time.Time
	path boardDirectionPath
}

const (
	beforeBoardStop = 1 << iota
	afterBoardStop
)

func tflBackfillFilter(
	liveEntries []*ctdf.DepartureBoard,
	livePaths map[*ctdf.DepartureBoard]boardDirectionPath,
	stopIDs map[string]struct{},
	boardType ctdf.BoardType,
	servicesByRef map[string]*ctdf.Service,
	stopAliases map[string]string,
	disruptedServices map[string]struct{},
) func(*ctdf.DepartureBoard) bool {
	canonicalStop := func(ref string) string {
		if canonical := stopAliases[ref]; canonical != "" {
			return canonical
		}
		return ref
	}
	serviceRef := func(journey *ctdf.Journey) string {
		service := servicesByRef[journey.ServiceRef]
		if service == nil {
			service = journey.Service
		}
		if service != nil && service.PrimaryIdentifier != "" {
			return service.PrimaryIdentifier
		}
		return journey.ServiceRef
	}

	liveByService := map[string][]timedDirectionPath{}
	for _, entry := range liveEntries {
		path := livePaths[entry]
		if entry.Type != ctdf.DepartureBoardRecordTypeRealtimeTracked || path.empty() {
			continue
		}
		if ref := serviceRef(entry.Journey); ref != "" {
			liveByService[ref] = append(liveByService[ref], timedDirectionPath{time: entry.Time, path: path})
		}
	}
	if len(liveByService) == 0 && len(disruptedServices) == 0 {
		return nil
	}
	for _, predictions := range liveByService {
		slices.SortFunc(predictions, func(a, b timedDirectionPath) int { return b.time.Compare(a.time) })
	}

	return func(entry *ctdf.DepartureBoard) bool {
		if entry.Type != ctdf.DepartureBoardRecordTypeScheduled || entry.Journey == nil {
			return true
		}
		service := serviceRef(entry.Journey)
		if _, disrupted := disruptedServices[service]; disrupted {
			return false
		}
		predictions := liveByService[service]
		if len(predictions) == 0 || entry.Time.After(predictions[0].time) {
			return true
		}
		path := scheduledBoardDirectionPath(entry, stopIDs, boardType)
		if path.empty() {
			return true
		}
		scheduledSides := make(map[string]int, len(path.before)+len(path.after))
		for _, ref := range path.before {
			scheduledSides[canonicalStop(ref)] |= beforeBoardStop
		}
		for _, ref := range path.after {
			scheduledSides[canonicalStop(ref)] |= afterBoardStop
		}

		// The live feed can omit adjacent stops. Compare the closest shared
		// call on each side and ignore calls appearing on both sides of a loop.
		matchSide := func(refs []string, side, oppositeSide int) int {
			for _, ref := range refs {
				switch scheduledSides[canonicalStop(ref)] {
				case side:
					return 1
				case oppositeSide:
					return -1
				}
			}
			return 0
		}
		for _, live := range predictions {
			if entry.Time.After(live.time) {
				break
			}
			before := matchSide(live.path.before, beforeBoardStop, afterBoardStop)
			after := matchSide(live.path.after, afterBoardStop, beforeBoardStop)
			// A common approach alone cannot establish which branch a train
			// departs towards. Arrivals require a match on the approach instead.
			sameDirection := after > 0 && before >= 0
			if boardType.IsArrival() {
				sameDirection = before > 0 && after >= 0
			}
			if sameDirection {
				return false
			}
		}
		return true
	}
}

// Load aliases for live stops in one query. Timetable calls may use a platform
// reference where the TfL prediction uses the parent station.
func loadDirectionStopAliases(paths map[*ctdf.DepartureBoard]boardDirectionPath) map[string]string {
	refs := map[string]struct{}{}
	for _, path := range paths {
		for _, ref := range path.before {
			refs[ref] = struct{}{}
		}
		for _, ref := range path.after {
			refs[ref] = struct{}{}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(refs))
	for ref := range refs {
		ids = append(ids, ref)
	}
	cursor, err := database.GetCollection("stops").Find(context.Background(), bson.M{"$or": bson.A{
		bson.M{"primaryidentifier": bson.M{"$in": ids}},
		bson.M{"otheridentifiers": bson.M{"$in": ids}},
		bson.M{"platforms.primaryidentifier": bson.M{"$in": ids}},
	}}, options.Find().SetProjection(bson.M{"primaryidentifier": 1, "otheridentifiers": 1, "platforms.primaryidentifier": 1}))
	if err != nil {
		log.Warn().Err(err).Msg("Failed to look up TfL board direction stop aliases")
		return nil
	}
	defer cursor.Close(context.Background())

	aliases := map[string]string{}
	platforms := map[string]int{}
	for cursor.Next(context.Background()) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			log.Warn().Err(err).Msg("Failed to decode TfL board direction stop aliases")
			continue
		}
		for _, ref := range stop.GetAllStopIDs() {
			// Prefer the parent when platform and station documents overlap.
			if current := aliases[ref]; current == "" || len(stop.Platforms) > platforms[ref] ||
				(len(stop.Platforms) == platforms[ref] && stop.PrimaryIdentifier < current) {
				aliases[ref] = stop.PrimaryIdentifier
				platforms[ref] = len(stop.Platforms)
			}
		}
	}
	if err := cursor.Err(); err != nil {
		log.Warn().Err(err).Msg("Failed to read TfL board direction stop aliases")
	}
	return aliases
}
