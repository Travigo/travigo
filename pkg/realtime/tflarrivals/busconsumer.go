package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const busArrivalCacheMaxAge = time.Minute
const maxBusFallbackEventAge = 2 * time.Minute
const minTrackerRetryDelay = time.Second
const maxTrackerRetryDelay = 30 * time.Second

type busArrivalLookup map[string][]ArrivalPrediction

var latestBusArrivalLookup = struct {
	sync.RWMutex
	updated time.Time
	lookup  busArrivalLookup
}{}
var busArrivalRefreshMutex sync.Mutex

type BusBatchConsumer struct {
	retryMu    sync.Mutex
	retryDelay time.Duration
}

func NewBusBatchConsumer() *BusBatchConsumer {
	return &BusBatchConsumer{}
}

type BusMonitorEvent struct {
	LocalID                  string    `json:"LocalID"`
	SubmittedAt              time.Time `json:"SubmittedAt"`
	Line                     string    `json:"Line"`
	DirectionRef             string    `json:"DirectionRef"`
	NumberPlate              string    `json:"NumberPlate"`
	OriginRef                string    `json:"OriginRef"`
	DestinationRef           string    `json:"DestinationRef"`
	OriginAimedDepartureTime string    `json:"OriginAimedDepartureTime"`
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
		if arrival.VehicleID == "" || arrival.TripID == "" {
			continue
		}
		lookup[arrival.VehicleID] = append(lookup[arrival.VehicleID], arrival)
	}

	return lookup
}

func identifyBusFromLookup(event BusMonitorEvent, lookup busArrivalLookup) (string, error) {
	if event.NumberPlate == "" {
		return "", fmt.Errorf("missing vehicle plate")
	}

	tripIDs := make(map[string]struct{})
	for _, arrival := range lookup[event.NumberPlate] {
		if event.Line != "" && !strings.EqualFold(strings.TrimSpace(event.Line), strings.TrimSpace(arrival.LineID)) {
			continue
		}
		if eventDirection, arrivalDirection := comparableTfLDirection(event.DirectionRef), comparableTfLDirection(arrival.Direction); eventDirection != "" && arrivalDirection != "" && eventDirection != arrivalDirection {
			continue
		}
		if event.DestinationRef != "" && normalizeTfLStopReference(event.DestinationRef) != normalizeTfLStopReference(arrival.DestinationNaptanID) {
			continue
		}
		tripIDs[arrival.TripID] = struct{}{}
	}

	if len(tripIDs) == 1 {
		for tripID := range tripIDs {
			return tripID, nil
		}
	}
	return "", fmt.Errorf("could not be identified from %d matching trips", len(tripIDs))
}

func comparableTfLDirection(direction string) string {
	direction = strings.ToLower(strings.TrimSpace(direction))
	switch direction {
	case "inbound", "outbound", "clockwise", "anticlockwise":
		return direction
	default:
		return ""
	}
}

func normalizeTfLStopReference(reference string) string {
	reference = strings.TrimSpace(reference)
	if strings.HasPrefix(strings.ToLower(reference), "gb-atco-") {
		reference = reference[len("gb-atco-"):]
	}
	return strings.ToUpper(reference)
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
	started := time.Now()
	payloads := batch.Payloads()
	tflTrackerCollection := database.GetCollection("tfl_tracker")
	operationsByPair := make(map[tflTrackerKey]mongo.WriteModel)
	validDeliveries := make([]rmq.Delivery, 0, len(payloads))
	var stale, unmatched int
	var batchTotal, batchSuccess int
	var oldestEventAge time.Duration

	for i, payload := range payloads {
		var event BusMonitorEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			ackBusDelivery(batch[i])
			continue
		}

		eventAge := time.Since(event.SubmittedAt)
		if event.SubmittedAt.IsZero() || eventAge > maxBusFallbackEventAge {
			stale++
			ackBusDelivery(batch[i])
			continue
		}
		batchTotal++
		if eventAge > oldestEventAge {
			oldestEventAge = eventAge
		}

		tripID, err := c.IdentifyBus(event)
		if err == nil {
			batchSuccess++
			key := tflTrackerKey{Line: event.Line, TripID: tripID}
			operationsByPair[key] = mongo.NewUpdateOneModel().
				SetFilter(bson.M{"line": key.Line, "tripid": key.TripID}).
				SetUpdate(bson.M{"$set": bson.M{"creationdatetime": time.Now()}}).
				SetUpsert(true)
			validDeliveries = append(validDeliveries, batch[i])
		} else {
			unmatched++
			log.Debug().Str("line", event.Line).Str("vehicle", event.NumberPlate).Err(err).Msg("TfL bus fallback did not match")
			ackBusDelivery(batch[i])
		}
	}

	operations := make([]mongo.WriteModel, 0, len(operationsByPair))
	for _, operation := range operationsByPair {
		operations = append(operations, operation)
	}
	if len(operations) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := tflTrackerCollection.BulkWrite(ctx, operations, options.BulkWrite().SetOrdered(false))
		cancel()
		if err != nil {
			log.Error().Err(err).
				Int("received", len(payloads)).
				Int("matched", len(validDeliveries)).
				Int("unique_tracker_updates", len(operations)).
				Int("unmatched", unmatched).
				Int("stale", stale).
				Dur("oldest_event_age", oldestEventAge).
				Dur("processing_time", time.Since(started)).
				Float64("identification_rate", rate(batchSuccess, batchTotal)).
				Msg("Failed to persist TfL bus tracker batch")
			c.retryMatchedDeliveries(validDeliveries)
			return
		}
		c.resetRetryDelay()
	}

	for _, delivery := range validDeliveries {
		if err := delivery.Ack(); err != nil {
			log.Error().Err(err).Msg("Failed to acknowledge persisted TfL bus tracker event")
		}
	}
	log.Info().
		Int("received", len(payloads)).
		Int("matched", len(validDeliveries)).
		Int("unique_tracker_updates", len(operations)).
		Int("unmatched", unmatched).
		Int("stale", stale).
		Dur("oldest_event_age", oldestEventAge).
		Dur("processing_time", time.Since(started)).
		Float64("identification_rate", rate(batchSuccess, batchTotal)).
		Msg("Processed TfL bus fallback batch")
}

func (c *BusBatchConsumer) retryMatchedDeliveries(deliveries []rmq.Delivery) {
	if len(deliveries) == 0 {
		return
	}

	c.retryMu.Lock()
	if c.retryDelay == 0 {
		c.retryDelay = minTrackerRetryDelay
	} else if c.retryDelay < maxTrackerRetryDelay {
		c.retryDelay *= 2
		if c.retryDelay > maxTrackerRetryDelay {
			c.retryDelay = maxTrackerRetryDelay
		}
	}
	nominalDelay := c.retryDelay
	c.retryMu.Unlock()
	// Jitter adjacent workers' retries so a shared Mongo outage does not cause
	// all five consumers to wake and write at the same instant.
	delay := nominalDelay/2 + time.Duration(rand.Int64N(int64(nominalDelay/2)+1))

	log.Warn().Dur("retry_delay", delay).Int("deliveries", len(deliveries)).Msg("Requeueing TfL bus tracker deliveries after Mongo failure")
	time.Sleep(delay)
	for _, delivery := range deliveries {
		if err := delivery.Push(); err != nil {
			log.Error().Err(err).Msg("Failed to requeue TfL bus tracker delivery")
		}
	}
}

func (c *BusBatchConsumer) resetRetryDelay() {
	c.retryMu.Lock()
	c.retryDelay = 0
	c.retryMu.Unlock()
}

func ackBusDelivery(delivery rmq.Delivery) {
	if err := delivery.Ack(); err != nil {
		log.Error().Err(err).Msg("Failed to acknowledge discarded TfL bus fallback event")
	}
}

func rate(success, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(success) / float64(total)
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
		SubmittedAt:              time.Now(),
	})

	pretty.Println(value, err)
}
