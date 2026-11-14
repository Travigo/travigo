package darwin

import (
	"context"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type darwinCall struct {
	stop               *ctdf.Stop
	arrival, departure time.Time
	activity           []ctdf.JourneyPathItemActivity
	platform           string
}

func scheduleClock(public, working string) time.Time {
	for _, raw := range []string{public, working} {
		raw = strings.TrimSuffix(strings.TrimSpace(raw), "H")
		for _, layout := range []string{"15:04:05", "15:04", "1504"} {
			if clock, err := time.Parse(layout, raw); err == nil {
				return clock.AddDate(-1, 0, 0)
			}
		}
	}
	return time.Time{}
}

func buildDarwinCalls(calls []darwinCall) []ctdf.JourneyCall {
	if len(calls) < 2 {
		return nil
	}
	var last time.Time
	for i := range calls {
		call := &calls[i]
		if call.arrival.IsZero() {
			call.arrival = call.departure
		}
		if call.departure.IsZero() {
			call.departure = call.arrival
		}
		if call.arrival.IsZero() {
			return nil
		}
		for _, clock := range []*time.Time{&call.arrival, &call.departure} {
			for clock.Before(last) {
				*clock = clock.AddDate(0, 0, 1)
			}
			last = *clock
		}
	}
	journeyCalls := make([]ctdf.JourneyCall, 0, len(calls))
	destination := calls[len(calls)-1].stop.PrimaryName
	for index, source := range calls {
		call := ctdf.JourneyCall{
			StopRef: source.stop.PrimaryIdentifier, Stop: source.stop,
			Arrival: source.arrival, Departure: source.departure, Platform: source.platform,
			Start: index == 0, End: index == len(calls)-1,
		}
		call.Activity = source.activity
		if !call.End && destination != "" {
			call.Outgoing = &ctdf.JourneyLeg{DestinationDisplay: destination}
		}
		journeyCalls = append(journeyCalls, call)
	}
	return journeyCalls
}

func darwinCallActivity(activity string) []ctdf.JourneyPathItemActivity {
	switch strings.TrimSpace(activity) {
	case "TB", "U":
		return []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup}
	case "TF", "D":
		return []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown}
	default:
		return nil
	}
}

func publishDarwinPath(ctx context.Context, run *ctdf.RealtimeJourney, calls []ctdf.JourneyCall, date string) error {
	collection := database.GetCollection("journeys")
	id := railutils.DarwinAmendmentID(run.Journey.PrimaryIdentifier, date)
	var base ctdf.Journey
	if err := collection.FindOne(ctx, bson.M{"primaryidentifier": run.Journey.PrimaryIdentifier}).Decode(&base); err != nil {
		return err
	}
	amended := base
	amended.PrimaryIdentifier = id
	amended.OtherIdentifiers = nil // Keep synthetic schedules out of TrainUID lookups.
	amended.Calls = append([]ctdf.JourneyCall(nil), calls...)
	amended.RefreshStopRefs()
	amended.DepartureTime = calls[0].Departure
	if calls[0].Outgoing != nil {
		amended.DestinationDisplay = calls[0].Outgoing.DestinationDisplay
	} else {
		amended.DestinationDisplay = ""
	}
	amended.Track, amended.TrackRef = nil, ""
	amended.ReplacesJourneyRefs = []string{base.PrimaryIdentifier}
	amended.AmendmentStopRefs = base.GetStopRefs()
	amended.Availability = &ctdf.Availability{Match: []ctdf.AvailabilityRule{{Type: ctdf.AvailabilityDate, Value: date}}}
	amended.Expiry = run.JourneyRunDate.AddDate(0, 0, 2)
	amended.DataSource = &ctdf.DataSourceReference{DatasetID: railutils.DarwinAmendmentDataset, ProviderName: "National Rail"}
	if err := amended.ValidatePath(); err != nil {
		return err
	}
	for _, name := range []string{database.JourneysRawCollectionName, database.JourneysCollectionName} {
		if _, err := database.GetCollection(name).ReplaceOne(ctx, bson.M{"primaryidentifier": id}, &amended, options.Replace().SetUpsert(true)); err != nil {
			return err
		}
	}
	updated := *run.Journey
	updated.ApplyRouteAmendment(&amended)
	remapDarwinStopState(run, &updated)
	run.Journey = &updated
	return nil
}

func remapDarwinStopState(run *ctdf.RealtimeJourney, updated *ctdf.Journey) {
	old := *run
	run.Stops = nil
	occurrences := map[string]int{}
	for i, call := range old.Journey.StopCalls() {
		ref := call.StopRef
		index := darwinJourneyStopOccurrenceIndex(updated, &ctdf.Stop{PrimaryIdentifier: ref}, occurrences[ref])
		occurrences[ref]++
		if previous := old.RealtimeStop(ref, i); previous != nil && index >= 0 {
			stop := *previous
			stop.JourneyStopIndex = index
			run.SetRealtimeStop(&stop)
		}
	}
	// The next location prediction will resolve the stop against the new path.
	run.NextStopRef, run.NextStopIndex = "", 0
}

// Keep the imported path and its geometry when the passenger calling pattern
// is unchanged. Timing predictions and cancellations still use realtime state.
func darwinPathChanged(before *ctdf.Journey, after []ctdf.JourneyCall) bool {
	if before == nil {
		return true
	}
	beforeIndex := 0
	var origin, destination *ctdf.JourneyCall
	var found bool
	for index := 0; index+1 < len(after); index++ {
		beforeIndex, origin, destination, found = nextDarwinJourneyLeg(before, beforeIndex)
		if !found || origin.StopRef != after[index].StopRef || destination.StopRef != after[index+1].StopRef {
			return true
		}
		beforeIndex++
	}
	_, _, _, found = nextDarwinJourneyLeg(before, beforeIndex)
	return found
}

func darwinJourneysPathChanged(before, after *ctdf.Journey) bool {
	if before == nil || after == nil {
		return true
	}
	beforeIndex, afterIndex := 0, 0
	var beforeOrigin, beforeDestination, afterOrigin, afterDestination *ctdf.JourneyCall
	var beforeFound, afterFound bool
	for {
		beforeIndex, beforeOrigin, beforeDestination, beforeFound = nextDarwinJourneyLeg(before, beforeIndex)
		afterIndex, afterOrigin, afterDestination, afterFound = nextDarwinJourneyLeg(after, afterIndex)
		if !beforeFound || !afterFound {
			return beforeFound != afterFound
		}
		if beforeOrigin.StopRef != afterOrigin.StopRef || beforeDestination.StopRef != afterDestination.StopRef {
			return true
		}
		beforeIndex++
		afterIndex++
	}
}

func nextDarwinJourneyLeg(journey *ctdf.Journey, start int) (int, *ctdf.JourneyCall, *ctdf.JourneyCall, bool) {
	if journey == nil {
		return 0, nil, nil, false
	}
	for index := start; index+1 < len(journey.Calls); index++ {
		origin, destination := &journey.Calls[index], &journey.Calls[index+1]
		if !origin.End && !destination.Start {
			return index, origin, destination, true
		}
	}
	return len(journey.Calls), nil, nil, false
}

// Darwin keys updates by run, so a cached run can outlive a timetable change.
func reconcileDarwinRun(ctx context.Context, run *ctdf.RealtimeJourney, id, uid, serviceDate string, now time.Time, source *ctdf.DataSourceReference) (*ctdf.RealtimeJourney, error) {
	date, err := time.Parse(ctdf.YearMonthDayFormat, serviceDate)
	if err != nil {
		return nil, err
	}
	collection := database.GetCollection("journeys")
	cursor, err := collection.Find(ctx, bson.M{"otheridentifiers.TrainUID": uid})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var journey *ctdf.Journey
	bestPriority := -1
	for cursor.Next(ctx) {
		var candidate ctdf.Journey
		if err := cursor.Decode(&candidate); err != nil {
			return nil, err
		}
		if candidate.Availability == nil || !candidate.Availability.MatchDate(date) ||
			(!candidate.Expiry.IsZero() && !candidate.Expiry.After(time.Now())) {
			continue
		}
		priority := 0
		if candidate.DataSource != nil && candidate.DataSource.DatasetID == railutils.VSTPDataset {
			priority = 2
		} else if strings.HasSuffix(candidate.PrimaryIdentifier, ":O") {
			priority = 1
		}
		if priority > bestPriority || (priority == bestPriority && candidate.PrimaryIdentifier > journey.PrimaryIdentifier) {
			journey, bestPriority = &candidate, priority
		}
	}
	if err := cursor.Err(); err != nil || journey == nil {
		return nil, err
	}
	var amendment ctdf.Journey
	err = collection.FindOne(ctx, bson.M{
		"primaryidentifier": railutils.DarwinAmendmentID(journey.PrimaryIdentifier, serviceDate),
		"expiry":            bson.M{"$gt": time.Now()},
	}).Decode(&amendment)
	if err == nil {
		journey.ApplyRouteAmendment(&amendment)
	} else if err != mongo.ErrNoDocuments {
		return nil, err
	}
	if run != nil && run.Journey != nil && run.Journey.PrimaryIdentifier == journey.PrimaryIdentifier &&
		run.JourneyRunDate.Format(ctdf.YearMonthDayFormat) == serviceDate {
		if darwinJourneysPathChanged(run.Journey, journey) {
			remapDarwinStopState(run, journey)
			run.Journey = journey
		}
		return run, nil
	}

	journey.GetService()
	if run == nil || run.JourneyRunDate.Format(ctdf.YearMonthDayFormat) != serviceDate {
		run = &ctdf.RealtimeJourney{
			PrimaryIdentifier: id, JourneyRunDate: date, CreationDateTime: now,
			TimeoutDurationMinutes: 181, Reliability: ctdf.RealtimeJourneyReliabilityExternalProvided,
			DataSource: source,
		}
	}
	// Predictions and cancellations for the previous schedule may refer to
	// calls that the replacement moved or removed.
	run.Journey, run.Service = journey, journey.Service
	run.Stops, run.Cancelled = nil, false
	run.NextStopRef, run.NextStopIndex = "", 0
	return run, nil
}
