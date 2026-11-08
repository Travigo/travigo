package localdepartureboard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liip/sheriff"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source/cachedresults"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"

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

	boardType := q.Type
	if boardType == "" {
		boardType = ctdf.BoardTypeDeparture
	}
	baseCacheItemPath := fmt.Sprintf("cachedresults/%sboardjourneys-v3/%s/%s", boardType, q.Stop.PrimaryIdentifier, filterHashString)
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
	staticQuery := bson.M{"$and": bson.A{journeyQuery, bson.M{"datasource.datasetid": bson.M{"$nin": bson.A{railutils.VSTPDataset, railutils.DarwinAmendmentDataset}}}}}
	dynamicQuery := bson.M{
		"datasource.datasetid": bson.M{"$in": bson.A{railutils.VSTPDataset, railutils.DarwinAmendmentDataset}},
		"expiry":               bson.M{"$gt": time.Now()},
		"$or": bson.A{
			bson.M{stopField: bson.M{"$in": allStopIDs}},
			bson.M{"amendmentstoprefs": bson.M{"$in": allStopIDs}},
		},
	}
	if q.Filter != nil {
		dynamicQuery["$and"] = bson.A{q.Filter}
	}
	dynamic, err := loadDynamicJourneys(dynamicQuery)
	if err != nil {
		return nil, err
	}
	// A call after midnight can belong to the previous service day. Search
	// the previous, selected and following days before sorting the board.
	// Performance Review [Set G] [PR018] [P1] (boards): Longer service-day
	// overflow and delays may require additional lookback; establish feed bounds.
	startOfDay := time.Date(q.StartDateTime.Year(), q.StartDateTime.Month(), q.StartDateTime.Day(), 0, 0, 0, 0, q.StartDateTime.Location())
	startOfTomorrow := startOfDay.AddDate(0, 0, 1)
	tomorrowServiceStart := time.Date(startOfTomorrow.Year(), startOfTomorrow.Month(), startOfTomorrow.Day(), 12, 0, 0, 0, startOfTomorrow.Location()).Add(-12 * time.Hour)
	for _, date := range []time.Time{startOfDay.AddDate(0, 0, -1), q.StartDateTime, startOfDay.AddDate(0, 0, 1)} {
		if date.Equal(startOfTomorrow) {
			beforeTomorrow := 0
			for _, entry := range departureBoard {
				if entry.Time.Before(tomorrowServiceStart) {
					beforeTomorrow++
				}
			}
			if beforeTomorrow >= q.Count {
				break
			}
		}
		journeys, fromGraph, err := s.getBoardJourneysWithSource(q, baseCacheItemPath, staticQuery, date, boardType)
		if err != nil {
			return nil, err
		}
		journeys = mergeJourneyAmendments(journeys, dynamic, date)
		lookup := s.realtimeLookup(journeys, date, allStopIDs)
		doEstimates := date.Format(ctdf.YearMonthDayFormat) == time.Now().In(date.Location()).Format(ctdf.YearMonthDayFormat)
		entries := ctdf.GenerateBoardFromJourneysForServiceDate(journeys, allStopIDs, date, q.StartDateTime, doEstimates, lookup, boardType)
		// Performance Review [Set K] [PR019] [P1]: Missing realtime data makes a valid scheduled-only
		// board repeat timetable loading, realtime lookup, and generation. Base fallback
		// on graph freshness/completeness or insufficient candidates instead, preserving
		// delayed departures and cancellation handling. Measure fallback frequency and
		// board latency before changing this behaviour.
		if fromGraph && !hasRealtimeBoardEntry(entries) {
			journeys, err = s.getDateJourneys(baseCacheItemPath, staticQuery, date)
			if err != nil {
				return nil, err
			}
			journeys = mergeJourneyAmendments(journeys, dynamic, date)
			lookup = s.realtimeLookup(journeys, date, allStopIDs)
			entries = ctdf.GenerateBoardFromJourneysForServiceDate(journeys, allStopIDs, date, q.StartDateTime, doEstimates, lookup, boardType)
		}
		departureBoard = append(departureBoard, entries...)
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
	journeys, _, _ := s.getBoardJourneysWithSource(q, baseCacheItemPath, journeyQuery, serviceDate, boardType)
	return journeys
}

func (s Source) getBoardJourneysWithSource(q query.DepartureBoard, baseCacheItemPath string, journeyQuery bson.M, serviceDate time.Time, boardType ctdf.BoardType) ([]*ctdf.Journey, bool, error) {
	if s.DepartureGraph != nil && q.Filter == nil && !boardType.IsArrival() {
		// Performance Review [Set E] [PR020] [P2] (boards): This fresh Background context permits
		// 30 seconds on the graph, followed by untimed Mongo/Redis fallback and
		// enrichment after the caller has stopped waiting. Thread one request budget
		// through the query and all stages, reserving time for fallback instead of
		// granting each dependency an independent full timeout.
		lookupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Board generation can retain calls scheduled up to four hours before
		// the requested time when realtime has delayed them.
		notBefore := serviceDate
		if !q.StartDateTime.IsZero() {
			notBefore = q.StartDateTime.Add(-4 * time.Hour)
		}
		limit := departureGraphCandidateLimit(q.Count)
		journeys, err := s.DepartureGraph.JourneysForStopWindow(
			lookupContext,
			q.Stop,
			serviceDate,
			notBefore,
			limit,
		)
		if err == nil && len(journeys) < limit {
			return journeys, true, nil
		}
		if err != nil {
			log.Warn().Err(err).Str("stop", q.Stop.PrimaryIdentifier).Time("service_date", serviceDate).Msg("Departure graph lookup failed; falling back to scheduled journey cache")
		} else {
			log.Debug().Str("stop", q.Stop.PrimaryIdentifier).Time("service_date", serviceDate).Int("candidates", len(journeys)).Msg("Departure graph candidate limit reached; falling back to scheduled journey cache")
		}
	}
	journeys, err := s.getDateJourneys(baseCacheItemPath, journeyQuery, serviceDate)
	return journeys, false, err
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
	// Performance Review [Set K] [PR021] [P2] (boards): A fixed 8x overfetch is not a completeness
	// guarantee after deduplication, activity/replacement filtering and realtime
	// reordering. One surviving live entry prevents fallback even if later candidates
	// could fill today's board. Return exhaustion/cursor information and fetch bounded
	// further windows until enough usable entries exist or the day is exhausted.
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

	realtimeJourneysByJourneyID, err := realtimestore.FindBoardRealtimeForJourneyIDs(context.Background(), journeyIDs)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query realtime journeys")
	}
	cancelledJourneyIDs, err := ctdf.ActiveJourneyCancellationAlertIDs(context.Background(), journeyIDs, serviceDate, time.Now())
	if err != nil {
		log.Error().Err(err).Msg("Failed to query journey cancellation service alerts")
	}
	if cancelledJourneyIDs == nil {
		cancelledJourneyIDs = map[string]struct{}{}
	}
	if err := addLegacyVSTPCancellations(context.Background(), realtimeJourneysByJourneyID, serviceDate, cancelledJourneyIDs); err != nil {
		log.Error().Err(err).Msg("Failed to check legacy VSTP cancellations")
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
			for id, journey := range realtimeJourneys {
				if journey == nil || journey.JourneyRunDate.IsZero() || journey.JourneyRunDate.Format(ctdf.YearMonthDayFormat) != serviceDate.Format(ctdf.YearMonthDayFormat) {
					delete(realtimeJourneys, id)
				}
			}
			return realtimeJourneys
		},
	}
}

// Older VSTP records have only a start and end date, not the weekday mask.
// Preserve their old range-wide cancellation behaviour until the feed writes
// an alert with the exact operating dates or the realtime record expires.
func addLegacyVSTPCancellations(ctx context.Context, realtime map[string]*ctdf.RealtimeJourney, serviceDate time.Time, cancelled map[string]struct{}) error {
	const realtimePrefix = "gb-nationalrail-vstp-cancellation-"
	const alertPrefix = "gb-networkrail-vstpcancel-"
	date := serviceDate.Format(ctdf.YearMonthDayFormat)
	journeysByAlert := map[string][]string{}
	for id, update := range realtime {
		if update == nil || !update.Cancelled {
			continue
		}
		suffix, ok := strings.CutPrefix(update.PrimaryIdentifier, realtimePrefix)
		if !ok {
			continue
		}
		parts := strings.SplitN(suffix, ":", 3)
		if len(parts) != 3 {
			continue
		}
		start, startErr := time.Parse(ctdf.YearMonthDayFormat, parts[0])
		end, endErr := time.Parse(ctdf.YearMonthDayFormat, parts[1])
		if startErr != nil || endErr != nil || start.After(end) || date < parts[0] || date > parts[1] {
			continue
		}
		alertID := alertPrefix + suffix
		journeysByAlert[alertID] = append(journeysByAlert[alertID], id)
	}
	if len(journeysByAlert) == 0 {
		return nil
	}
	alertIDs := make([]string, 0, len(journeysByAlert))
	for id := range journeysByAlert {
		alertIDs = append(alertIDs, id)
	}
	cursor, err := database.GetCollection("service_alerts").Find(ctx, bson.M{"primaryidentifier": bson.M{"$in": alertIDs}}, options.Find().SetProjection(bson.M{"_id": 0, "primaryidentifier": 1}))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var alert struct{ PrimaryIdentifier string }
		if err := cursor.Decode(&alert); err != nil {
			return err
		}
		delete(journeysByAlert, alert.PrimaryIdentifier)
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	for _, ids := range journeysByAlert {
		for _, id := range ids {
			cancelled[id] = struct{}{}
		}
	}
	return nil
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

func (s Source) getDateJourneys(baseCacheItemPath string, journeyQuery bson.M, dateTime time.Time) ([]*ctdf.Journey, error) {
	loadStart := time.Now()
	journeys := make([]*ctdf.Journey, 0, 50)

	cacheItemPath := fmt.Sprintf("%s/%s", baseCacheItemPath, dateTime.Format("2006-01-02"))

	// Performance Review [Set K] [PR022] [P2]: Concurrent misses for this stop/date independently
	// query MongoDB, decode schedules, check availability, and compress the result.
	// Coalesce loads by cache key; consider a short cross-instance lease if needed.
	// Share only immutable timetable data, since board generation attaches realtime
	// state per request, and invalidate/version entries when the timetable changes.
	journeys, err := cachedresults.Get[[]*ctdf.Journey](s.CachedResults, cacheItemPath)

	if err == nil {
		log.Debug().
			Str("cache_item", cacheItemPath).
			Time("date", dateTime).
			Int("journeys", len(journeys)).
			Dur("duration", time.Since(loadStart)).
			Msg("Departure board journey cache hit")
		return journeys, nil
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
		bson.E{Key: "path.distance", Value: 0},
		bson.E{Key: "path.originstop", Value: 0},
		bson.E{Key: "path.destinationstop", Value: 0},
	})

	cursor, err := journeysCollection.Find(context.Background(), journeyQuery, opts)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query Journeys")
		return nil, fmt.Errorf("query board journeys: %w", err)
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
			return nil, fmt.Errorf("decode board journey: %w", err)
		}
		if journey.Availability == nil {
			log.Warn().Str("journey", journey.PrimaryIdentifier).Msg("Skipping board journey without availability")
			continue
		}
		decodedJourneys++

		if journey.Availability.MatchDate(dateTime) {
			journeys = append(journeys, &journey)
			dateMatchedJourneys++
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("read board journeys: %w", err)
	}

	log.Debug().
		Str("cache_item", cacheItemPath).
		Time("date", dateTime).
		Int("decoded_journeys", decodedJourneys).
		Int("date_matched_journeys", dateMatchedJourneys).
		Dur("duration", time.Since(currentTime)).
		Msg("Departure board journey database decode complete")

	writeCacheTime := time.Now()
	reducedJourneys, err := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"departureboard-cache"},
	}, journeys)
	if err != nil {
		return nil, fmt.Errorf("encode board journeys: %w", err)
	}

	cachedresults.Set(s.CachedResults, cacheItemPath, reducedJourneys, 18*time.Hour)
	log.Debug().
		Str("cache_item", cacheItemPath).
		Int("cached_journeys", len(journeys)).
		Dur("marshal_and_write_duration", time.Since(writeCacheTime)).
		Dur("total_duration", time.Since(loadStart)).
		Msg("Departure board journey cache write complete")

	return journeys, nil
}

func loadDynamicJourneys(filter bson.M) ([]*ctdf.Journey, error) {
	ctx := context.Background()
	collection := database.GetCollection("journeys")
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("query dynamic journeys: %w", err)
	}
	defer cursor.Close(ctx)
	var journeys []*ctdf.Journey
	if err := cursor.All(ctx, &journeys); err != nil {
		return nil, fmt.Errorf("read dynamic journeys: %w", err)
	}
	// An in-flight Darwin write can land after a VSTP withdrawal. Require
	// each amendment's parent to still exist before using the amended route.
	var parentIDs []string
	for _, journey := range journeys {
		if journey.DataSource.DatasetID == railutils.DarwinAmendmentDataset && len(journey.ReplacesJourneyRefs) == 1 {
			parentIDs = append(parentIDs, journey.ReplacesJourneyRefs[0])
		}
	}
	if len(parentIDs) == 0 {
		return journeys, nil
	}
	parents, err := collection.Distinct(ctx, "primaryidentifier", bson.M{
		"primaryidentifier": bson.M{"$in": parentIDs},
		"$or":               bson.A{bson.M{"expiry": bson.M{"$exists": false}}, bson.M{"expiry": bson.M{"$gt": time.Now()}}},
	})
	if err != nil {
		return nil, fmt.Errorf("check journey amendment parents: %w", err)
	}
	active := make(map[string]bool, len(parents))
	for _, parent := range parents {
		if id, ok := parent.(string); ok {
			active[id] = true
		}
	}
	valid := journeys[:0]
	for _, journey := range journeys {
		if journey.DataSource.DatasetID != railutils.DarwinAmendmentDataset ||
			(len(journey.ReplacesJourneyRefs) == 1 && active[journey.ReplacesJourneyRefs[0]]) {
			valid = append(valid, journey)
		}
	}
	return valid, nil
}

func mergeJourneyAmendments(base, dynamic []*ctdf.Journey, date time.Time) []*ctdf.Journey {
	// Keep the source so a Darwin amendment to the original train cannot
	// bring it back when a VSTP overlay replaces it.
	replaced := map[string]string{}
	var active []*ctdf.Journey
	merged := make([]*ctdf.Journey, 0, len(base)+len(dynamic))
	for _, journey := range dynamic {
		if journey.Availability == nil || !journey.Availability.MatchDate(date) {
			continue
		}
		active = append(active, journey)
		for _, id := range journey.ReplacesJourneyRefs {
			if replaced[id] != railutils.VSTPDataset {
				replaced[id] = journey.DataSource.DatasetID
			}
		}
	}
	for _, journey := range base {
		if journey.DataSource != nil {
			switch journey.DataSource.DatasetID {
			case railutils.VSTPDataset, railutils.DarwinAmendmentDataset:
				continue
			}
		}
		if replaced[journey.PrimaryIdentifier] == "" {
			merged = append(merged, journey)
		}
	}
	for _, journey := range active {
		if replaced[journey.PrimaryIdentifier] != "" {
			continue
		}
		copy := *journey
		if journey.DataSource.DatasetID == railutils.DarwinAmendmentDataset && len(journey.ReplacesJourneyRefs) == 1 {
			copy.PrimaryIdentifier = journey.ReplacesJourneyRefs[0]
			if replaced[copy.PrimaryIdentifier] == railutils.VSTPDataset {
				continue
			}
		}
		merged = append(merged, &copy)
	}
	return merged
}
