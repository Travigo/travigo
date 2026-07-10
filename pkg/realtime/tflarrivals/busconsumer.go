package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/adjust/rmq/v5"
)

var totalEvents atomic.Uint64
var successEvents atomic.Uint64

const busArrivalCacheMaxAge = time.Minute

type busArrivalLookup map[string]map[string]string

var latestBusArrivalLookup = struct {
	sync.RWMutex
	updated time.Time
	lookup  busArrivalLookup
}{}
var busArrivalRefreshMutex sync.Mutex

type BusBatchConsumer struct {
}

func NewBusBatchConsumer() *BusBatchConsumer {
	return &BusBatchConsumer{}
}

type BusMonitorEvent struct {
	Line                     string
	DirectionRef             string
	NumberPlate              string
	OriginRef                string
	DestinationRef           string
	OriginAimedDepartureTime string
}

type TflTracker struct {
	Line             string
	TripID           string
	CreationDateTime time.Time
}

type tflTrackerKey struct {
	Line   string
	TripID string
}

func loadTFLTrackerRuntimeJourneyFilter(ctx context.Context) (RuntimeJourneyFilter, error) {
	tflTrackerCollection := database.GetCollection("tfl_tracker")
	cursor, err := tflTrackerCollection.Find(ctx, bson.M{
		"creationdatetime": bson.M{"$gte": time.Now().Add(-4 * time.Hour)},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var trackers []TflTracker
	if err := cursor.All(ctx, &trackers); err != nil {
		return nil, err
	}

	filter := newTFLTrackerRuntimeJourneyFilter(trackers)
	log.Info().Int("records", len(trackers)).Msg("Loaded TfL bus runtime journey filter")
	return filter, nil
}

func newTFLTrackerRuntimeJourneyFilter(trackers []TflTracker) RuntimeJourneyFilter {
	trackedJourneys := make(map[tflTrackerKey]struct{}, len(trackers))
	for _, tracker := range trackers {
		trackedJourneys[tflTrackerKey{Line: tracker.Line, TripID: tracker.TripID}] = struct{}{}
	}

	return func(lineID string, tripID string) bool {
		_, tracked := trackedJourneys[tflTrackerKey{Line: lineID, TripID: tripID}]
		return tracked
	}
}

func cacheBusArrivals(arrivals []ArrivalPrediction) {
	lookup := buildBusArrivalLookup(arrivals)

	latestBusArrivalLookup.Lock()
	latestBusArrivalLookup.updated = time.Now()
	latestBusArrivalLookup.lookup = lookup
	latestBusArrivalLookup.Unlock()
}

func cachedBusArrivalLookup() (busArrivalLookup, bool) {
	latestBusArrivalLookup.RLock()
	defer latestBusArrivalLookup.RUnlock()

	if latestBusArrivalLookup.lookup == nil || time.Since(latestBusArrivalLookup.updated) > busArrivalCacheMaxAge {
		return nil, false
	}

	return latestBusArrivalLookup.lookup, true
}

func buildBusArrivalLookup(arrivals []ArrivalPrediction) busArrivalLookup {
	lookup := make(busArrivalLookup)
	for _, arrival := range arrivals {
		realtimeJourneyID := fmt.Sprintf(
			"realtime-tfl-%s-%s-%s-%s-%s",
			arrival.ModeName,
			arrival.LineID,
			arrival.Direction,
			arrival.VehicleID,
			arrival.DestinationNaptanID,
		)

		vehicleJourneys := lookup[arrival.VehicleID]
		if vehicleJourneys == nil {
			vehicleJourneys = make(map[string]string)
			lookup[arrival.VehicleID] = vehicleJourneys
		}
		vehicleJourneys[realtimeJourneyID] = arrival.TripID
	}

	return lookup
}

func identifyBusFromLookup(event BusMonitorEvent, lookup busArrivalLookup) (string, error) {
	vehicleJourneys := lookup[event.NumberPlate]
	if len(vehicleJourneys) == 1 {
		for _, tripID := range vehicleJourneys {
			return tripID, nil
		}
	}

	return "", fmt.Errorf("Could not be identified. Count: %d", len(vehicleJourneys))
}

func currentBusArrivalLookup() busArrivalLookup {
	if arrivalLookup, cached := cachedBusArrivalLookup(); cached {
		return arrivalLookup
	}

	// Only one queue consumer should perform the fallback download if the main
	// tracker has not populated or refreshed the shared cache yet.
	busArrivalRefreshMutex.Lock()
	defer busArrivalRefreshMutex.Unlock()

	if arrivalLookup, cached := cachedBusArrivalLookup(); cached {
		return arrivalLookup
	}

	lineTracker := ModeArrivalTracker{Mode: &TfLMode{ModeID: "bus"}}
	lineTracker.GetLatestArrivals()
	arrivalLookup, _ := cachedBusArrivalLookup()
	return arrivalLookup
}

func (c *BusBatchConsumer) Consume(batch rmq.Deliveries) {
	payloads := batch.Payloads()

	tflTrackerCollection := database.GetCollection("tfl_tracker")

	for _, payload := range payloads {
		var event BusMonitorEvent
		err := json.Unmarshal([]byte(payload), &event)

		if err != nil {
			continue
		}

		totalEvents.Add(1)

		tripID, err := c.IdentifyBus(event)
		if err == nil {
			successEvents.Add(1)
			log.Info().Str("tripid", tripID).Str("line", event.Line).Msg("Identified")

			tflTracker := TflTracker{
				Line:             event.Line,
				TripID:           tripID,
				CreationDateTime: time.Now(),
			}

			tflTrackerCollection.InsertOne(context.Background(), tflTracker)
		} else {
			log.Info().Interface("event", event).Err(err).Msg("Failed to identify")
		}
	}

	log.Info().Float64("rate", float64(successEvents.Load())/float64(totalEvents.Load())).Int("total", int(totalEvents.Load())).Int("success", int(successEvents.Load())).Msg("Events rate")

	if ackErrors := batch.Ack(); len(ackErrors) > 0 {
		for _, err := range ackErrors {
			log.Fatal().Err(err).Msg("Failed to consume from queue")
		}
	}
}

func (c *BusBatchConsumer) IdentifyBus(event BusMonitorEvent) (string, error) {
	return identifyBusFromLookup(event, currentBusArrivalLookup())
}

func (c *BusBatchConsumer) Test() {
	value, err := c.IdentifyBus(BusMonitorEvent{
		Line:                     "269",
		DirectionRef:             "2",
		NumberPlate:              "LJ11ABV",
		OriginRef:                "gb-atco-490003975E",
		DestinationRef:           "gb-atco-490003975L",
		OriginAimedDepartureTime: "2024-08-23T21:53:00+00:00",
	})

	pretty.Println(value, err)
}
