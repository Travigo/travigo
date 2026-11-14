package routes

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func JourneysRouter(router fiber.Router) {
	router.Get("/:identifier/stops/:stop_identifier/door-side", getJourneyStopDoorSide)
	router.Get("/:identifier", getJourney)
}

func getJourney(c *fiber.Ctx) error {
	if err := validateJourneyPathFormat(c); err != nil {
		return sheriffViewError(c, err)
	}
	identifier := c.Params("identifier")
	realtimeOnly := c.QueryBool("realtime_only", false)
	views := sheriffViews{
		"web": {"web-journey"},
	}
	if !realtimeOnly {
		views["saved"] = []string{"web-saved"}
		views["notification"] = []string{"web-notification"}
	}
	if err := validateSheriffView(c, views); err != nil {
		return sheriffViewError(c, err)
	}

	var journey *ctdf.Journey
	journey, err := dataaggregator.Lookup[*ctdf.Journey](query.Journey{
		PrimaryIdentifier: identifier,
	})

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {

		realtimeJourney, realtimeErr := realtimestore.FindCurrentForJourney(context.Background(), journey.PrimaryIdentifier)
		if realtimeErr != nil {
			log.Error().Err(realtimeErr).Str("journey", journey.PrimaryIdentifier).Msg("Failed to query realtime journey")
		}
		if date := c.Query("date"); date != "" && journey.OtherIdentifiers["TrainUID"] != "" && realtimeJourney != nil && realtimeJourney.JourneyRunDate.Format(ctdf.YearMonthDayFormat) != date {
			realtimeJourney = nil
		}
		if realtimeJourney == nil {
			realtimeJourney = findBlockJourneyEstimate(c.UserContext(), journey, journeyServiceDate(c, journey))
		}
		if !realtimeOnly {
			applyRailAmendment(c, journey, realtimeJourney)
			hydrateJourneyReferences(journey)
		}
		journey.RealtimeJourney = realtimeJourney
		if realtimeJourney != nil && realtimeJourney.Journey != nil {
			realtimeJourney.Journey.GetTracks()
		}

		var journeyReduced interface{}

		if realtimeOnly {
			if journey.RealtimeJourney == nil {
				journeyReduced = nil
			} else {
				journeyReduced, err = marshalJourneyView(c, journey.RealtimeJourney, []string{"basic", "detailed"}, sheriffViews{
					"web": {"web-journey-realtime"},
				})
			}
		} else {
			applyJourneyServiceStopNameOverrides(journey)

			journeyReduced, err = marshalJourneyView(c, journey, []string{"basic", "detailed"}, sheriffViews{
				"web":          {"web-journey"},
				"saved":        {"web-saved"},
				"notification": {"web-notification"},
			})
		}

		if err != nil {
			return sheriffViewError(c, err)
		}

		return c.JSON(journeyReduced)
	}
}

func journeyServiceDate(c *fiber.Ctx, journey *ctdf.Journey) time.Time {
	location := time.Local
	if journey != nil && journey.DepartureTimezone != "" {
		if loaded, err := time.LoadLocation(journey.DepartureTimezone); err == nil {
			location = loaded
		}
	}
	if date := c.Query("date"); date != "" {
		if parsed, err := time.ParseInLocation(ctdf.YearMonthDayFormat, date, location); err == nil {
			return parsed
		}
	}
	return time.Now().In(location)
}

// findBlockJourneyEstimate supplies journey-view stop estimates when the
// journey itself has no realtime record but its vehicle is finishing an earlier
// journey in the same block.
func findBlockJourneyEstimate(ctx context.Context, journey *ctdf.Journey, serviceDate time.Time) *ctdf.RealtimeJourney {
	if journey == nil || journey.OtherIdentifiers["BlockNumber"] == "" || journey.DepartureTime.IsZero() {
		return nil
	}
	blockQuery := bson.M{
		"otheridentifiers.BlockNumber": journey.OtherIdentifiers["BlockNumber"],
		"departuretime":                bson.M{"$lt": journey.DepartureTime},
	}
	if journey.DataSource != nil && journey.DataSource.DatasetID != "" {
		blockQuery["datasource.datasetid"] = journey.DataSource.DatasetID
	} else {
		blockQuery["serviceref"] = journey.ServiceRef
	}
	cursor, err := database.GetCollection("journeys").Find(ctx, blockQuery, options.Find().
		SetProjection(bson.D{
			{Key: "_id", Value: 0},
			{Key: "primaryidentifier", Value: 1},
		}).SetSort(bson.D{{Key: "departuretime", Value: -1}}))
	if err != nil {
		log.Error().Err(err).Str("journey", journey.PrimaryIdentifier).Msg("Failed to query preceding block journeys")
		return nil
	}
	defer cursor.Close(ctx)

	var preceding []struct {
		PrimaryIdentifier string `bson:"primaryidentifier"`
	}
	if err := cursor.All(ctx, &preceding); err != nil {
		log.Error().Err(err).Str("journey", journey.PrimaryIdentifier).Msg("Failed to read preceding block journeys")
		return nil
	}
	journeyIDs := make([]string, 0, len(preceding))
	for _, previous := range preceding {
		if previous.PrimaryIdentifier != "" {
			journeyIDs = append(journeyIDs, previous.PrimaryIdentifier)
		}
	}
	if len(journeyIDs) == 0 {
		return nil
	}
	realtimeByJourneyID, err := realtimestore.FindCurrentForJourneyIDs(ctx, journeyIDs)
	if err != nil {
		log.Error().Err(err).Str("journey", journey.PrimaryIdentifier).Msg("Failed to query preceding block realtime journeys")
		return nil
	}
	var incoming *ctdf.RealtimeJourney
	for _, previous := range preceding {
		if realtime := realtimeByJourneyID[previous.PrimaryIdentifier]; realtime != nil {
			if !realtime.JourneyRunDate.IsZero() && realtime.JourneyRunDate.Format(ctdf.YearMonthDayFormat) != serviceDate.Format(ctdf.YearMonthDayFormat) {
				continue
			}
			incoming = realtime
			break
		}
	}
	if incoming == nil || incoming.Offset <= 0 {
		return nil
	}
	target := &ctdf.DepartureBoard{Journey: journey, ServiceDate: serviceDate.Format(ctdf.YearMonthDayFormat), Time: journey.DepartureTime}
	if !ctdf.BlockDelayCarriesIntoTarget(incoming, target) {
		return nil
	}
	return blockRealtimeEstimate(journey, incoming.Offset, serviceDate)
}

func blockRealtimeEstimate(journey *ctdf.Journey, offset time.Duration, serviceDate time.Time) *ctdf.RealtimeJourney {
	if journey == nil || journey.LegCount() == 0 {
		return nil
	}
	realtimeJourneySchedule := *journey
	realtimeJourneySchedule.RealtimeJourney = nil
	realtime := &ctdf.RealtimeJourney{
		PrimaryIdentifier: fmt.Sprintf("block-estimate:%s:%s", journey.PrimaryIdentifier, serviceDate.Format(ctdf.YearMonthDayFormat)),
		Journey:           &realtimeJourneySchedule,
		JourneyRunDate:    serviceDate,
		ActivelyTracked:   true,
		Offset:            offset,
		Stops:             make(map[string]*ctdf.RealtimeJourneyStops),
	}
	pathIndex := 0
	for origin, destination := range journey.LegCalls() {
		departure := ctdf.ScheduledTimeOnServiceDate(journey, pathIndex, serviceDate, ctdf.BoardTypeDeparture)
		arrival := ctdf.ScheduledTimeOnServiceDate(journey, pathIndex, serviceDate, ctdf.BoardTypeArrival)
		if !departure.IsZero() {
			originStop := realtime.RealtimeStop(origin.StopRef, pathIndex)
			if originStop == nil {
				originStop = &ctdf.RealtimeJourneyStops{StopRef: origin.StopRef, JourneyStopIndex: pathIndex}
			}
			originStop.DepartureTime = departure.Add(offset)
			originStop.TimeType = ctdf.RealtimeJourneyStopTimeEstimatedFuture
			realtime.SetRealtimeStop(originStop)
		}
		if !arrival.IsZero() {
			destinationStop := realtime.RealtimeStop(destination.StopRef, pathIndex+1)
			if destinationStop == nil {
				destinationStop = &ctdf.RealtimeJourneyStops{StopRef: destination.StopRef, JourneyStopIndex: pathIndex + 1}
			}
			destinationStop.ArrivalTime = arrival.Add(offset)
			destinationStop.TimeType = ctdf.RealtimeJourneyStopTimeEstimatedFuture
			realtime.SetRealtimeStop(destinationStop)
		}
		pathIndex++
	}
	return realtime
}

func applyJourneyServiceStopNameOverrides(journey *ctdf.Journey) {
	if journey == nil || journey.Service == nil || len(journey.Service.StopNameOverrides) == 0 {
		return
	}

	for index := range journey.Calls {
		stop := journey.Calls[index].Stop
		if stop == nil {
			continue
		}
		for _, stopID := range stop.GetAllStopIDs() {
			name := journey.Service.StopNameOverrides[stopID]
			if name == "" {
				continue
			}
			if name != stop.PrimaryName {
				adapted := *stop
				adapted.PrimaryName = name
				journey.Calls[index].Stop = &adapted
			}
			break
		}
	}
}

func applyRailAmendment(c *fiber.Ctx, journey *ctdf.Journey, realtime *ctdf.RealtimeJourney) {
	if journey.OtherIdentifiers["TrainUID"] == "" {
		return
	}
	date := c.Query("date")
	if date == "" && realtime != nil {
		date = realtime.JourneyRunDate.Format(ctdf.YearMonthDayFormat)
	}
	if date == "" {
		if loc, err := time.LoadLocation(journey.DepartureTimezone); err == nil {
			date = time.Now().In(loc).Format(ctdf.YearMonthDayFormat)
		}
	}
	if _, err := time.Parse(ctdf.YearMonthDayFormat, date); err != nil {
		return
	}
	var amendment ctdf.Journey
	err := database.GetCollection("journeys").FindOne(c.UserContext(), bson.M{
		"primaryidentifier": railutils.DarwinAmendmentID(journey.PrimaryIdentifier, date),
		"expiry":            bson.M{"$gt": time.Now()},
	}).Decode(&amendment)
	if err == nil {
		journey.ApplyRouteAmendment(&amendment)
	} else if err != mongo.ErrNoDocuments {
		log.Error().Err(err).Str("journey", journey.PrimaryIdentifier).Msg("Failed to load Darwin route amendment")
	}
}
