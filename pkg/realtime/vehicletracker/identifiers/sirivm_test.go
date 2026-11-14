package identifiers

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func siriTestCalls(origin, destination string, originArrival, departure, destinationArrival time.Time) bson.A {
	return bson.A{
		bson.M{"stopref": origin, "start": true, "arrival": originArrival, "departure": departure},
		bson.M{"stopref": destination, "end": true, "arrival": destinationArrival},
	}
}

func TestSameServiceNameNormalizesCommonPresentationDifferences(t *testing.T) {
	for _, test := range []struct{ left, right string }{
		{" X 1 ", "x-1"},
		{"A/12", "a12"},
		{"25", "Route 25"},
	} {
		if !sameServiceName(test.left, test.right) {
			t.Fatalf("%q and %q should match", test.left, test.right)
		}
	}
}

func TestSiriReportsCandidatesForEachAttemptedFallback(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("all fallbacks", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previous })
		operatorRef := mt.Name() + time.Now().String()
		available := bson.D{
			{Key: "primaryidentifier", Value: "available"},
			{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
			{Key: "direction", Value: "outbound"},
			{Key: "calls", Value: siriTestCalls("origin", "dest", time.Time{}, time.Date(0, 1, 2, 1, 30, 0, 0, time.UTC), time.Time{})},
		}
		unavailable := bson.D{
			{Key: "primaryidentifier", Value: "unavailable"},
			{Key: "calls", Value: siriTestCalls("origin", "dest", time.Time{}, time.Time{}, time.Time{})},
		}
		wrongDirection := bson.D{
			{Key: "primaryidentifier", Value: "wrong-direction"},
			{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
			{Key: "direction", Value: "inbound"},
			{Key: "departuretime", Value: time.Date(0, 1, 2, 1, 30, 0, 0, time.UTC)},
			{Key: "calls", Value: siriTestCalls("origin", "dest", time.Time{}, time.Date(0, 1, 2, 1, 30, 0, 0, time.UTC), time.Time{})},
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".operators", mtest.FirstBatch,
				bson.D{{Key: "primaryidentifier", Value: operatorRef}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".services", mtest.FirstBatch,
				bson.D{{Key: "primaryidentifier", Value: "service"}, {Key: "servicename", Value: "1"}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, unavailable),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, wrongDirection, available, unavailable),
		)

		before := telemetry.Snapshot().SiriMatching
		identifier := SiriVM{IdentifyingInformation: map[string]string{
			"OperatorRef": operatorRef, "PublishedLineName": "1", "VehicleJourneyRef": "trip",
			"BlockRef": "block", "OriginRef": "origin", "FramedVehicleJourneyDate": "2026-11-10",
			"DirectionRef": "outbound", "OriginAimedDepartureTime": "2026-11-10T01:30:00+00:00",
		}}
		if got, err := identifier.IdentifyJourney(); err != nil || got != "available" {
			mt.Fatalf("stops fallback returned %q, %v", got, err)
		}
		after := telemetry.Snapshot().SiriMatching
		for name, want := range map[string][3]int64{
			"journey_ref": {1, 0, 0},
			"block_ref":   {0, 0, 0},
			"stops":       {3, 2, 1},
		} {
			a, b := before[name], after[name]
			if b.Lookup.Count-a.Lookup.Count != 1 || b.Narrowing.Count-a.Narrowing.Count != 1 {
				mt.Errorf("%s did not record one lookup and narrowing attempt", name)
			}
			if got := [3]int64{b.Candidates - a.Candidates, b.Available - a.Available, b.Matches - a.Matches}; got != want {
				mt.Errorf("%s candidates, available and matches = %v, want %v", name, got, want)
			}
		}
		commands := mt.GetAllStartedEvents()
		if len(commands) != 6 {
			mt.Fatalf("sent %d MongoDB commands, want 6", len(commands))
		}
		for n, field := range []string{"otheridentifiers.GTFS-TripID", "otheridentifiers.TicketMachineJourneyCode"} {
			filter := commands[n+2].Command.Lookup("filter").Document()
			if filter.Lookup(field).StringValue() != "trip" || filter.Lookup("serviceref", "$in", "0").StringValue() != "service" {
				mt.Fatalf("unexpected reference filter %v", filter)
			}
		}
		filter := commands[5].Command.Lookup("filter").Document()
		if filter.Lookup("originstoprefs").StringValue() != "origin" {
			mt.Fatalf("unexpected stop filter %v", filter)
		}
		if _, err := filter.LookupErr("$expr"); err == nil {
			mt.Fatalf("stop lookup still has a departure-time expression: %v", filter)
		}
		if got, err := identifier.IdentifyJourney(); err != nil || got != "available" || len(mt.GetAllStartedEvents()) != len(commands) {
			mt.Fatalf("repeat lookup did not reuse the match: %q, %v", got, err)
		}
	})
}

func TestSameServiceNameDoesNotMatchDifferentRoutes(t *testing.T) {
	if sameServiceName("25", "26") {
		t.Fatal("different service names matched")
	}
}

func TestSiriStopLookupsFetchOnlyIntersectingJourneys(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("both stops", func(mt *mtest.T) {
		namespace := mt.DB.Name() + "." + mt.Coll.Name()
		id := primitive.NewObjectID()
		idRow := func(id primitive.ObjectID) bson.D { return bson.D{{Key: "_id", Value: id}} }
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, idRow(id), idRow(primitive.NewObjectID())),
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, idRow(primitive.NewObjectID()), idRow(id)),
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, bson.D{
				{Key: "primaryidentifier", Value: "journey"},
				{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
				{Key: "calls", Value: siriTestCalls("a", "c", time.Time{}, time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(0, 1, 1, 9, 10, 0, 0, time.UTC))},
			}),
		)

		query := bson.M{"serviceref": bson.M{"$in": []string{"service"}}, "originstoprefs": "a", "destinationstoprefs": "c"}
		got, err := getSiriStopJourneys(mt.Coll, time.Now(), query, time.Time{}, "")
		if err != nil || len(got.available) != 1 || got.available[0].PrimaryIdentifier != "journey" {
			mt.Fatalf("unexpected candidates: %+v, %v", got, err)
		}
		commands := mt.GetAllStartedEvents()
		if len(commands) != 3 {
			mt.Fatalf("sent %d commands, want two ID scans and one fetch", len(commands))
		}
		for n, branch := range []struct{ field, stop, index string }{
			{"originstoprefs", "a", database.JourneyOriginStopServiceIDsIndex},
			{"destinationstoprefs", "c", database.JourneyDestinationStopServiceIDsIndex},
		} {
			command := commands[n].Command
			filter := command.Lookup("filter").Document()
			projection, err := command.Lookup("projection").Document().Elements()
			if filter.Lookup(branch.field).StringValue() != branch.stop || command.Lookup("hint").StringValue() != branch.index ||
				err != nil || len(projection) != 1 || projection[0].Key() != "_id" || projection[0].Value().Int32() != 1 {
				mt.Fatalf("unexpected stop ID lookup: %v", command)
			}
		}
		fetch := commands[2].Command
		filter := fetch.Lookup("filter").Document()
		values, err := filter.Lookup("_id", "$in").Array().Values()
		if err != nil || len(values) != 1 || values[0].ObjectID() != id || fetch.Lookup("hint").StringValue() != "_id_" ||
			filter.Lookup("originstoprefs").StringValue() != "a" || filter.Lookup("destinationstoprefs").StringValue() != "c" {
			mt.Fatalf("unexpected candidate fetch: %v", fetch)
		}
	})
	mt.Run("index missing", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 2, Message: "hint provided does not correspond to an existing index"}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch),
		)
		query := bson.M{"serviceref": "service", "originstoprefs": "a", "destinationstoprefs": "c"}
		if _, err := getSiriStopJourneys(mt.Coll, time.Now(), query, time.Time{}, ""); err != nil {
			mt.Fatal(err)
		}
		fallback := mt.GetAllStartedEvents()[1].Command
		if _, err := fallback.LookupErr("hint"); err == nil {
			mt.Fatalf("fallback still requires a hint: %v", fallback)
		}
	})
}

func TestSiriReferenceMissesAreReusedAcrossUpdates(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("empty reference", func(mt *mtest.T) {
		day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
		services := []string{mt.Name() + time.Now().String()}
		identifier := SiriVM{CurrentTime: day}
		namespace := mt.DB.Name() + "." + mt.Coll.Name()
		empty := mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch)
		mt.AddMockResponses(empty, mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 50}), empty, empty)
		if _, err := identifier.findReferencedJourney(mt.Coll, day, services, "reference"); err == nil || isSiriNoMatch(err) {
			mt.Fatalf("failed reference query returned %v, want database error", err)
		}
		if _, err := identifier.findReferencedJourney(mt.Coll, day, services, "reference"); err != errSiriJourney {
			mt.Fatalf("retry returned %v, want no match", err)
		}
		identifier.IdentifyingInformation = map[string]string{"DirectionRef": "inbound"}
		if _, err := identifier.findReferencedJourney(mt.Coll, day.AddDate(0, 0, 1), services, "reference"); err != errSiriJourney {
			mt.Fatalf("repeat reference returned %v, want cached miss", err)
		}
		if got := len(mt.GetAllStartedEvents()); got != 4 {
			mt.Fatalf("sent %d commands, want two queries per uncached attempt", got)
		}
	})
}

func TestSiriReferenceMatchesAreNarrowedTogether(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("reference fields", func(mt *mtest.T) {
		day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
		identifier := SiriVM{CurrentTime: day, IdentifyingInformation: map[string]string{
			"OriginAimedDepartureTime": "2026-11-10T10:00:00+00:00",
		}}
		candidate := func(id string, hour int) bson.D {
			departure := time.Date(0, 1, 1, hour, 0, 0, 0, time.UTC)
			return bson.D{
				{Key: "primaryidentifier", Value: id},
				{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
				{Key: "departuretime", Value: departure},
				{Key: "calls", Value: siriTestCalls("origin", "dest", departure, departure, departure.Add(10*time.Minute))},
			}
		}
		namespace := mt.DB.Name() + "." + mt.Coll.Name()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, candidate("gtfs", 9)),
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, candidate("ticket-machine", 10)),
		)
		journey, err := identifier.findReferencedJourney(mt.Coll, day, []string{mt.Name()}, "reference")
		if err != nil || journey == nil || journey.PrimaryIdentifier != "ticket-machine" {
			mt.Fatalf("got %v, %v, want the ticket-machine candidate at 10:00", journey, err)
		}
	})
}

func TestSiriExactDepartureAcceptsOvernightClockAndRejectsAmbiguity(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("overnight departure", func(mt *mtest.T) {
		day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
		aimed := day.Add(90 * time.Minute)
		identifier := SiriVM{CurrentTime: day, IdentifyingInformation: map[string]string{
			"OriginAimedDepartureTime": "2026-11-11T01:30:00+00:00", "DirectionRef": "OUTBOUND",
		}}
		candidate := func(id string) bson.D {
			departure := time.Date(0, 1, 2, 1, 30, 0, 0, time.UTC)
			return bson.D{
				{Key: "primaryidentifier", Value: id},
				{Key: "direction", Value: "outbound"},
				{Key: "departuretime", Value: departure},
				{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
				{Key: "calls", Value: siriTestCalls("stop", "dest", departure, departure, departure.Add(10*time.Minute))},
			}
		}
		namespace := mt.DB.Name() + "." + mt.Coll.Name()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, candidate("trip")),
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, candidate("trip"), candidate("other")),
		)
		query := bson.M{"serviceref": "route", "originstoprefs": "stop"}
		candidates, err := getAvailableJourneys(mt.Coll, day, query, aimed)
		if err != nil {
			mt.Fatal(err)
		}
		if got := identifier.exactDepartureMatch(candidates, aimed); got == nil || got.PrimaryIdentifier != "trip" {
			mt.Fatalf("exact departure got %v, want overnight trip", got)
		}
		candidates, err = getAvailableJourneys(mt.Coll, day, query, aimed)
		if err != nil {
			mt.Fatal(err)
		}
		if got := identifier.exactDepartureMatch(candidates, aimed); got != nil {
			mt.Fatalf("ambiguous departure selected %q instead of allowing the broad fallback", got.PrimaryIdentifier)
		}
	})
}

func TestSiriCandidateProjectionRetainsMatchingFields(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("candidate narrowing", func(mt *mtest.T) {
		day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
		candidate := func(id string, hour int) bson.D {
			departure := time.Date(0, 1, 1, hour, 0, 0, 0, time.UTC)
			return bson.D{
				{Key: "primaryidentifier", Value: id},
				{Key: "serviceref", Value: "service"},
				{Key: "direction", Value: "outbound"},
				{Key: "departuretime", Value: departure},
				{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
				{Key: "calls", Value: siriTestCalls("origin", "dest", departure, departure, departure.Add(10*time.Minute))},
			}
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, candidate("wanted", 10), candidate("other", 11)))
		loaded, err := getAvailableJourneys(mt.Coll, day, bson.M{"serviceref": "service"}, time.Time{})
		if err != nil {
			mt.Fatal(err)
		}
		candidates := loaded.available
		projection := mt.GetStartedEvent().Command.Lookup("projection").Document()
		for _, field := range []string{
			"primaryidentifier", "serviceref", "direction", "departuretime", "availability",
			"calls.stopref", "calls.start", "calls.end", "calls.arrival", "calls.departure", "calls.incomingarrival",
		} {
			if projection.Lookup(field).Int32() != 1 {
				mt.Fatalf("candidate projection omits required field %s", field)
			}
		}
		if _, err := projection.LookupErr("track"); err == nil {
			mt.Fatal("candidate projection includes whole-journey geometry")
		}
		identifier := SiriVM{
			CurrentTime: day,
			IdentifyingInformation: map[string]string{
				"DirectionRef": "outbound", "OriginRef": "origin", "DestinationRef": "dest",
				"OriginAimedDepartureTime": "2026-11-10T10:00:00+00:00",
			},
		}
		selected, err := identifier.narrowJourneys(candidates, true)
		if err != nil || selected.PrimaryIdentifier != "wanted" {
			mt.Fatalf("lightweight candidates changed matching: %+v (%v)", selected, err)
		}
		if len(candidates) != 2 || candidates[0].Availability == nil || candidates[0].Calls[0].StopRef != "origin" {
			mt.Fatal("candidate availability or ordered path was lost")
		}
		if candidates[0].GenerateFunctionalHash(true) == candidates[1].GenerateFunctionalHash(true) {
			mt.Fatal("candidate projection lost functional hash inputs")
		}
	})
}

func TestSiriStopProbeKeepsExistingMatch(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, scenario := range []struct {
		name      string
		ambiguous bool
	}{
		{name: "unique"},
		{name: "ambiguous", ambiguous: true},
	} {
		mt.Run(scenario.name, func(mt *mtest.T) {
			ns := mt.DB.Name() + "." + mt.Coll.Name()
			id := primitive.NewObjectID()
			ids := mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: id}})
			day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
			aimed := day.Add(90 * time.Minute)
			row := func(id string) bson.D {
				departure := time.Date(0, 1, 2, 1, 30, 0, 0, time.UTC)
				return bson.D{
					{Key: "primaryidentifier", Value: id},
					{Key: "direction", Value: "outbound"},
					{Key: "departuretime", Value: departure},
					{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}},
					{Key: "calls", Value: siriTestCalls("a", "b", departure, departure, departure.Add(10*time.Minute))},
				}
			}
			rows := []bson.D{row("trip")}
			if scenario.ambiguous {
				rows = append(rows, row("other"))
			}
			candidates := mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, rows...)
			mt.AddMockResponses(ids, ids, candidates, candidates, candidates)
			query := bson.M{"serviceref": "route", "originstoprefs": "a", "destinationstoprefs": "b"}
			got, err := getSiriStopJourneys(mt.Coll, day, query, aimed, "OUTBOUND")
			if err != nil {
				mt.Fatal(err)
			}
			wantCommands := 3
			if scenario.ambiguous {
				wantCommands++
			}
			commands := mt.GetAllStartedEvents()
			if len(commands) != wantCommands {
				mt.Fatalf("got %d commands, want %d", len(commands), wantCommands)
			}
			probe := commands[2].Command
			if _, err := probe.LookupErr("projection", "calls"); err == nil {
				mt.Fatal("exact probe fetched path fields")
			}
			if _, err := probe.LookupErr("filter", "$expr"); err != nil {
				mt.Fatal("exact probe did not constrain departure time")
			}
			baseline, err := getAvailableJourneys(mt.Coll, day, query, aimed)
			if err != nil {
				mt.Fatal(err)
			}
			match := func(c siriJourneyCandidates) string {
				if j := exactSiriDepartureMatch(c, aimed, "OUTBOUND"); j != nil {
					return j.PrimaryIdentifier
				}
				return ""
			}
			if match(got) != match(baseline) || len(got.available) != len(baseline.available) {
				mt.Fatalf("probe result differs from baseline: %v vs %v", got, baseline)
			}
		})
	}
}

func TestSiriStopProbeUsesNumericDirectionFallbackOnlyWhenNoActivePreferredJourney(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, scenario := range []struct {
		name      string
		preferred string
		active    bool
		want      string
	}{
		{name: "GTFS boolean direction", want: "exact"},
		{name: "inactive preferred direction", preferred: "1", want: "exact"},
		{name: "active off-time preferred direction", preferred: "1", active: true, want: "preferred"},
	} {
		mt.Run(scenario.name, func(mt *mtest.T) {
			ns := mt.DB.Name() + "." + mt.Coll.Name()
			ids := mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: primitive.NewObjectID()}})
			day := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
			aimed := day.Add(9*time.Hour + 30*time.Minute)
			row := func(id, direction string, departure time.Time, available bool) bson.D {
				fields := bson.D{{Key: "primaryidentifier", Value: id}, {Key: "direction", Value: direction}, {Key: "departuretime", Value: departure}}
				if available {
					fields = append(fields, bson.E{Key: "availability", Value: bson.M{"match": bson.A{bson.M{"type": "MatchAll"}}}})
				}
				fields = append(fields, bson.E{Key: "calls", Value: siriTestCalls("a", "b", departure, departure, departure.Add(10*time.Minute))})
				return fields
			}
			rows := []bson.D{row("exact", "false", aimed, true)}
			if scenario.preferred != "" {
				rows = append(rows, row("preferred", scenario.preferred, aimed.Add(time.Minute), scenario.active))
			}
			response := mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, rows...)
			mt.AddMockResponses(ids, ids, response, response, response)
			query := bson.M{"serviceref": "service", "originstoprefs": "a", "destinationstoprefs": "b"}
			got, err := getSiriStopJourneys(mt.Coll, day, query, aimed, "1")
			if err != nil {
				mt.Fatal(err)
			}
			commands := mt.GetAllStartedEvents()
			wantQueries := 3
			if scenario.active {
				wantQueries = 4
			}
			if len(commands) != wantQueries {
				mt.Fatalf("got %d queries, want %d", len(commands), wantQueries)
			}
			if _, err := commands[2].Command.LookupErr("filter", "$expr", "$or"); err != nil {
				mt.Fatal("probe must include off-time journeys in the preferred direction")
			}
			baseline, err := getAvailableJourneys(mt.Coll, day, query, aimed)
			if err != nil {
				mt.Fatal(err)
			}
			matcher := SiriVM{CurrentTime: day, IdentifyingInformation: map[string]string{
				"DirectionRef": "1", "OriginAimedDepartureTime": aimed.Format(ctdf.XSDDateTimeFormat),
			}}
			for _, candidates := range []siriJourneyCandidates{got, baseline} {
				match, err := matcher.selectJourney(telemetry.SiriStops, 0, candidates, nil, aimed)
				if err != nil || match.PrimaryIdentifier != scenario.want {
					mt.Fatalf("match = %v, error = %v, want %q", match, err, scenario.want)
				}
			}
		})
	}
}
