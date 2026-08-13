package localdepartureboard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liip/sheriff"
	"github.com/rs/zerolog/log"
	iso8601 "github.com/senseyeio/duration"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source/cachedresults"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"

	// "github.com/travigo/travigo/pkg/transforms"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	departureGraphCandidateMultiplier = 8
	departureGraphMinimumCandidates   = 128
	departureGraphMaximumCandidates   = 20000
)

func (s Source) DepartureBoardQuery(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
	q.Type = ctdf.BoardTypeDeparture
	return s.BoardQuery(q)
}

func (s Source) BoardQuery(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
	queryStart := time.Now()
	var departureBoard []*ctdf.DepartureBoard

	// Calculate tomorrows start date time by shifting current date time by 1 day and then setting hours/minutes/seconds to 0
	nextDayDuration, _ := iso8601.ParseISO8601("P1D")
	dayAfterDateTime := nextDayDuration.Shift(q.StartDateTime)
	dayAfterDateTime = time.Date(
		dayAfterDateTime.Year(), dayAfterDateTime.Month(), dayAfterDateTime.Day(), 0, 0, 0, 0, dayAfterDateTime.Location(),
	)

	// Contains the stops primary id and all platforms primary ids
	allStopIDs := q.Stop.GetAllStopIDs()

	// Load from cache
	var filterHashString string
	if q.Filter == nil {
		filterHashString = "nofilter"
	} else {
		filterBytes, _ := json.Marshal(q.Filter)
		filterHash := sha256.New()
		filterHash.Write(filterBytes)
		filterHashString = fmt.Sprintf("%x", filterHash.Sum(nil))
	}

	currentTime := time.Now()

	boardType := q.Type
	if boardType == "" {
		boardType = ctdf.BoardTypeDeparture
	}
	baseCacheItemPath := fmt.Sprintf("cachedresults/%sboardjourneys/%s/%s", boardType, q.Stop.PrimaryIdentifier, filterHashString)
	stopField := "path.originstopref"
	if boardType.IsArrival() {
		stopField = "path.destinationstopref"
	}
	journeyQuery := bson.M{stopField: bson.M{"$in": allStopIDs}}
	if q.Filter != nil {
		journeyQuery = bson.M{
			"$and": bson.A{
				journeyQuery,
				q.Filter,
			},
		}
	}
	journeysToday, journeysTodayFromGraph := s.getBoardJourneysWithSource(q, baseCacheItemPath, journeyQuery, q.StartDateTime, boardType)

	log.Debug().
		Str("stop", q.Stop.PrimaryIdentifier).
		Int("requested_count", q.Count).
		Int("stop_ids", len(allStopIDs)).
		Int("journeys", len(journeysToday)).
		Time("date", q.StartDateTime).
		Dur("duration", time.Since(currentTime)).
		Msg("Departure board journeys loaded - today")

	currentTime = time.Now()
	realtimeLookupToday := s.realtimeLookup(journeysToday, q.StartDateTime, allStopIDs)
	realtimeLookupTodayDuration := time.Since(currentTime)

	currentTime = time.Now()
	departureBoardToday := ctdf.GenerateBoardFromJourneys(journeysToday, allStopIDs, q.StartDateTime, true, realtimeLookupToday, boardType)
	if journeysTodayFromGraph && !hasRealtimeBoardEntry(departureBoardToday) {
		log.Warn().
			Str("stop", q.Stop.PrimaryIdentifier).
			Msg("Departure graph board produced no realtime entries; falling back to scheduled journey cache")
		journeysToday = s.getDateJourneys(baseCacheItemPath, journeyQuery, q.StartDateTime)
		realtimeLookupToday = s.realtimeLookup(journeysToday, q.StartDateTime, allStopIDs)
		departureBoardToday = ctdf.GenerateBoardFromJourneys(journeysToday, allStopIDs, q.StartDateTime, true, realtimeLookupToday, boardType)
	}
	log.Debug().
		Str("stop", q.Stop.PrimaryIdentifier).
		Int("journeys", len(journeysToday)).
		Int("departures", len(departureBoardToday)).
		Int("prefetched_realtime_journeys", len(realtimeLookupToday.ByJourneyID)).
		Dur("realtime_lookup_duration", realtimeLookupTodayDuration).
		Dur("generation_duration", time.Since(currentTime)).
		Msg("Departure board generation complete - today")

	// If not enough journeys in todays departure board then look into tomorrows
	if len(departureBoardToday) < q.Count {
		currentTime = time.Now()

		journeysTomorrow, journeysTomorrowFromGraph := s.getBoardJourneysWithSource(q, baseCacheItemPath, journeyQuery, dayAfterDateTime, boardType)

		log.Debug().
			Str("stop", q.Stop.PrimaryIdentifier).
			Int("requested_count", q.Count).
			Int("today_departures", len(departureBoardToday)).
			Int("journeys", len(journeysTomorrow)).
			Time("date", dayAfterDateTime).
			Dur("duration", time.Since(currentTime)).
			Msg("Departure board journeys loaded - tomorrow")

		currentTime = time.Now()
		realtimeLookupTomorrow := s.realtimeLookup(journeysTomorrow, dayAfterDateTime, allStopIDs)
		realtimeLookupTomorrowDuration := time.Since(currentTime)

		currentTime = time.Now()
		departureBoardTomorrow := ctdf.GenerateBoardFromJourneys(journeysTomorrow, allStopIDs, dayAfterDateTime, false, realtimeLookupTomorrow, boardType)
		if journeysTomorrowFromGraph && !hasRealtimeBoardEntry(departureBoardTomorrow) {
			log.Warn().
				Str("stop", q.Stop.PrimaryIdentifier).
				Msg("Departure graph tomorrow board produced no realtime entries; falling back to scheduled journey cache")
			journeysTomorrow = s.getDateJourneys(baseCacheItemPath, journeyQuery, dayAfterDateTime)
			realtimeLookupTomorrow = s.realtimeLookup(journeysTomorrow, dayAfterDateTime, allStopIDs)
			departureBoardTomorrow = ctdf.GenerateBoardFromJourneys(journeysTomorrow, allStopIDs, dayAfterDateTime, false, realtimeLookupTomorrow, boardType)
		}
		log.Debug().
			Str("stop", q.Stop.PrimaryIdentifier).
			Int("journeys", len(journeysTomorrow)).
			Int("departures", len(departureBoardTomorrow)).
			Int("prefetched_realtime_journeys", len(realtimeLookupTomorrow.ByJourneyID)).
			Dur("realtime_lookup_duration", realtimeLookupTomorrowDuration).
			Dur("generation_duration", time.Since(currentTime)).
			Msg("Departure board generation complete - tomorrow")

		departureBoard = append(departureBoardToday, departureBoardTomorrow...)
	} else {
		departureBoard = departureBoardToday
	}

	log.Debug().
		Str("stop", q.Stop.PrimaryIdentifier).
		Int("requested_count", q.Count).
		Int("departures", len(departureBoard)).
		Dur("total_duration", time.Since(queryStart)).
		Msg("Departure board source query complete")

	return departureBoard, nil
}

func (s Source) getBoardJourneys(q query.DepartureBoard, baseCacheItemPath string, journeyQuery bson.M, serviceDate time.Time, boardType ctdf.BoardType) []*ctdf.Journey {
	journeys, _ := s.getBoardJourneysWithSource(q, baseCacheItemPath, journeyQuery, serviceDate, boardType)
	return journeys
}

func (s Source) getBoardJourneysWithSource(q query.DepartureBoard, baseCacheItemPath string, journeyQuery bson.M, serviceDate time.Time, boardType ctdf.BoardType) ([]*ctdf.Journey, bool) {
	if s.DepartureGraph != nil && q.Filter == nil && !boardType.IsArrival() {
		lookupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		journeys, err := s.DepartureGraph.JourneysForStopWindow(
			lookupContext,
			q.Stop,
			serviceDate,
			serviceDate,
			departureGraphCandidateLimit(q.Count),
		)
		if err == nil {
			return journeys, true
		}
		log.Warn().
			Err(err).
			Str("stop", q.Stop.PrimaryIdentifier).
			Time("service_date", serviceDate).
			Msg("Departure graph lookup failed; falling back to scheduled journey cache")
	}
	return s.getDateJourneys(baseCacheItemPath, journeyQuery, serviceDate), false
}

func hasRealtimeBoardEntry(entries []*ctdf.DepartureBoard) bool {
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if entry.Type == ctdf.DepartureBoardRecordTypeRealtimeTracked || entry.Type == ctdf.DepartureBoardRecordTypeCancelled {
			return true
		}
		if entry.Journey != nil && entry.Journey.RealtimeJourney != nil {
			return true
		}
	}
	return false
}

func departureGraphCandidateLimit(count int) int {
	limit := count * departureGraphCandidateMultiplier
	if limit < departureGraphMinimumCandidates {
		limit = departureGraphMinimumCandidates
	}
	if limit > departureGraphMaximumCandidates {
		limit = departureGraphMaximumCandidates
	}
	return limit
}

func (s Source) realtimeLookup(journeys []*ctdf.Journey, serviceDate time.Time, stopRefs []string) *ctdf.DepartureBoardRealtimeLookup {
	lookupStart := time.Now()
	journeyIDs := make([]string, 0, len(journeys))
	for _, journey := range journeys {
		if journey == nil {
			continue
		}
		journeyIDs = append(journeyIDs, journey.PrimaryIdentifier)
	}

	realtimeJourneysByJourneyID, err := realtimestore.FindCurrentForJourneyIDs(context.Background(), journeyIDs)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query realtime journeys")
	}
	cancelledJourneyIDs, err := ctdf.ActiveJourneyCancellationAlertIDs(context.Background(), journeyIDs, serviceDate, time.Now())
	if err != nil {
		log.Error().Err(err).Msg("Failed to query journey cancellation service alerts")
	}

	log.Debug().
		Int("journeys", len(journeys)).
		Int("journey_ids", len(journeyIDs)).
		Int("realtime_journeys", len(realtimeJourneysByJourneyID)).
		Int("cancelled_by_alert", len(cancelledJourneyIDs)).
		Dur("duration", time.Since(lookupStart)).
		Msg("Prefetched realtime journeys for departure board")
	var stopAliases map[string][]string
	if len(realtimeJourneysByJourneyID) > 0 {
		stopAliases = findBoardStopAliases(stopRefs)
	}

	return &ctdf.DepartureBoardRealtimeLookup{
		ByJourneyID:         realtimeJourneysByJourneyID,
		CancelledJourneyIDs: cancelledJourneyIDs,
		StopAliases:         stopAliases,
		FindByJourneyIDs: func(journeyRefs []string) map[string]*ctdf.RealtimeJourney {
			realtimeJourneys, err := realtimestore.FindCurrentForJourneyIDs(context.Background(), journeyRefs)
			if err != nil {
				log.Error().Err(err).Msg("Failed to batch query realtime journeys by journey refs")
			}
			return realtimeJourneys
		},
	}
}

func findBoardStopAliases(stopRefs []string) map[string][]string {
	aliasesByRef, requestedRefs := boardStopAliasFallback(stopRefs)
	if len(requestedRefs) == 0 {
		return aliasesByRef
	}
	identifiers := make([]string, 0, len(requestedRefs))
	for stopRef := range requestedRefs {
		identifiers = append(identifiers, stopRef)
	}

	opts := options.Find().SetProjection(bson.D{
		{Key: "_id", Value: 0},
		{Key: "primaryidentifier", Value: 1},
		{Key: "otheridentifiers", Value: 1},
	})
	cursor, err := database.GetCollection("stops").Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$in": identifiers}},
			bson.M{"otheridentifiers": bson.M{"$in": identifiers}},
		},
	}, opts)
	if err != nil {
		log.Error().Err(err).Int("stop_refs", len(identifiers)).Msg("Failed to batch query departure board stop aliases")
		return aliasesByRef
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			log.Error().Err(err).Msg("Failed to decode departure board stop aliases")
			continue
		}
		stopAliases := make([]string, 0, 1+len(stop.OtherIdentifiers))
		stopAliases = append(stopAliases, stop.PrimaryIdentifier)
		stopAliases = append(stopAliases, stop.OtherIdentifiers...)
		for _, stopID := range stopAliases {
			if _, requested := requestedRefs[stopID]; requested {
				aliasesByRef[stopID] = stopAliases
			}
		}
	}
	if err := cursor.Err(); err != nil {
		log.Error().Err(err).Msg("Failed while reading departure board stop aliases")
	}
	return aliasesByRef
}

func boardStopAliasFallback(stopRefs []string) (map[string][]string, map[string]struct{}) {
	aliasesByRef := make(map[string][]string, len(stopRefs))
	requestedRefs := make(map[string]struct{}, len(stopRefs))
	allRequestedRefs := make([]string, 0, len(stopRefs))
	for _, stopRef := range stopRefs {
		if stopRef == "" {
			continue
		}
		if _, exists := requestedRefs[stopRef]; exists {
			continue
		}
		requestedRefs[stopRef] = struct{}{}
		allRequestedRefs = append(allRequestedRefs, stopRef)
	}

	// Keep the complete set of identifiers supplied for the board as the
	// fallback alias set. The database enrichment below is best-effort, and
	// platform boards can contain identifiers represented on the parent stop
	// rather than in the top-level identifier fields. Without this fallback, a
	// failed or incomplete alias query silently reduces realtime matching to
	// exact stop references.
	for _, stopRef := range allRequestedRefs {
		aliasesByRef[stopRef] = append([]string(nil), allRequestedRefs...)
	}
	return aliasesByRef, requestedRefs
}

func (s Source) getDateJourneys(baseCacheItemPath string, journeyQuery bson.M, dateTime time.Time) []*ctdf.Journey {
	loadStart := time.Now()
	journeys := make([]*ctdf.Journey, 0, 50)

	cacheItemPath := fmt.Sprintf("%s/%s", baseCacheItemPath, dateTime.Format("2006-01-02"))

	journeys, err := cachedresults.Get[[]*ctdf.Journey](s.CachedResults, cacheItemPath)

	if err == nil {
		log.Debug().
			Str("cache_item", cacheItemPath).
			Time("date", dateTime).
			Int("journeys", len(journeys)).
			Dur("duration", time.Since(loadStart)).
			Msg("Departure board journey cache hit")
		return journeys
	}

	journeysCollection := database.GetCollection("journeys")
	currentTime := time.Now()

	// This projection excludes values we dont care about - the main ones being path.*
	// Reduces memory usage and execution time
	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
		bson.E{Key: "creationdatetime", Value: 0},
		bson.E{Key: "modificationdatetime", Value: 0},
		bson.E{Key: "direction", Value: 0},
		bson.E{Key: "path.track", Value: 0},
		bson.E{Key: "path.associations", Value: 0},
		bson.E{Key: "path.destinationactivity", Value: 0},
		bson.E{Key: "path.distance", Value: 0},
		bson.E{Key: "path.originstop", Value: 0},
		bson.E{Key: "path.destinationstop", Value: 0},
		bson.E{Key: "detailedrailinformation", Value: 0},
	})

	cursor, err := journeysCollection.Find(context.Background(), journeyQuery, opts)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query Journeys")
		return journeys
	}
	defer cursor.Close(context.Background())

	log.Debug().
		Str("cache_item", cacheItemPath).
		Time("date", dateTime).
		Dur("duration", time.Since(currentTime)).
		Msg("Departure board journey database query complete")
	currentTime = time.Now()

	decodedJourneys := 0
	dateMatchedJourneys := 0
	for cursor.Next(context.Background()) {
		var journey ctdf.Journey
		err := cursor.Decode(&journey)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode journey")
		}
		decodedJourneys++

		if journey.Availability.MatchDate(dateTime) {
			journeys = append(journeys, &journey)
			dateMatchedJourneys++
		}
	}

	log.Debug().
		Str("cache_item", cacheItemPath).
		Time("date", dateTime).
		Int("decoded_journeys", decodedJourneys).
		Int("date_matched_journeys", dateMatchedJourneys).
		Dur("duration", time.Since(currentTime)).
		Msg("Departure board journey database decode complete")

	writeCacheTime := time.Now()
	reducedJourneys, _ := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"departureboard-cache"},
	}, journeys)

	cachedresults.Set(s.CachedResults, cacheItemPath, reducedJourneys, 18*time.Hour)
	log.Debug().
		Str("cache_item", cacheItemPath).
		Int("cached_journeys", len(journeys)).
		Dur("marshal_and_write_duration", time.Since(writeCacheTime)).
		Dur("total_duration", time.Since(loadStart)).
		Msg("Departure board journey cache write complete")

	return journeys
}
