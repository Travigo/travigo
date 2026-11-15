package vehicletracker

import (
	"slices"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestLocationFallbackFiltersServiceDateAndBatchesTracks(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("cold and warm geometry", func(mt *mtest.T) {
		oldDatabase := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = oldDatabase })

		asDocument := func(value any) bson.D {
			mt.Helper()
			encoded, err := bson.Marshal(value)
			if err != nil {
				mt.Fatal(err)
			}
			var document bson.D
			if err := bson.Unmarshal(encoded, &document); err != nil {
				mt.Fatal(err)
			}
			return document
		}
		candidate := func(id, wholeTrack, legTrack string, availability *ctdf.Availability) bson.D {
			return asDocument(&ctdf.Journey{
				PrimaryIdentifier: id,
				Availability:      availability,
				TrackRef:          wholeTrack,
				Calls: ctdf.JourneyCallsFromLegs(&ctdf.JourneyPathItem{
					OriginStopRef: "A", DestinationStopRef: "B", TrackRef: legTrack,
				}),
			})
		}
		point := func(x, y float64) ctdf.Location {
			return ctdf.Location{Type: "Point", Coordinates: []float64{x, y}}
		}
		track := func(ref string, latitude float64) bson.D {
			return asDocument(&ctdf.JourneyTrack{
				PrimaryIdentifier: ref,
				Track:             []ctdf.Location{point(0, latitude), point(0.01, latitude)},
			})
		}
		legRef, wholeRef := mt.Name()+":leg", mt.Name()+":whole"
		journeys := mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch,
			candidate("sunday-only", mt.Name()+":inactive", "", &ctdf.Availability{
				Match: []ctdf.AvailabilityRule{{Type: ctdf.AvailabilityDayOfWeek, Value: "Sunday"}},
			}),
			candidate("winner", wholeRef, legRef, nil),
			candidate("farther", wholeRef, "", nil),
		)
		mt.AddMockResponses(journeys, mtest.CreateCursorResponse(0, mt.DB.Name()+".journey_tracks", mtest.FirstBatch,
			track(legRef, 51), track(wholeRef, 51.001),
		), journeys)

		date := time.Date(2026, time.November, 16, 12, 0, 0, 0, time.UTC)
		for _, attempt := range []string{"cold", "warm"} {
			if got := matchLocationJourney([]string{"service"}, date, point(0.005, 51), nil); got != "winner" {
				mt.Fatalf("%s geometry: matched %q, want winner", attempt, got)
			}
		}
		events := mt.GetAllStartedEvents()
		var collections []string
		for _, event := range events {
			collections = append(collections, event.Command.Lookup("find").StringValue())
		}
		if want := []string{"journeys", "journey_tracks", "journeys"}; !slices.Equal(collections, want) {
			mt.Fatalf("queried collections %v, want %v", collections, want)
		}
		values, err := events[1].Command.Lookup("filter", "primaryidentifier", "$in").Array().Values()
		if err != nil {
			mt.Fatal(err)
		}
		if len(values) != 2 || values[0].StringValue() != legRef || values[1].StringValue() != wholeRef {
			mt.Fatalf("track references %v, want the two distinct eligible references", values)
		}
	})
}
