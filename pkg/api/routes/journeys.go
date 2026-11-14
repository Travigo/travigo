package routes

import (
	"context"
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
