package routes

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/transforms"
)

func JourneysRouter(router fiber.Router) {
	router.Get("/:identifier/stops/:stop_identifier/door-side", getJourneyStopDoorSide)
	router.Get("/:identifier", getJourney)
}

func getJourney(c *fiber.Ctx) error {
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
		journey.GetReferences()
		journey.GetDeepReferences()

		realtimeJourney, realtimeErr := realtimestore.FindCurrentForJourney(context.Background(), journey.PrimaryIdentifier)
		if realtimeErr != nil {
			log.Error().Err(realtimeErr).Str("journey", journey.PrimaryIdentifier).Msg("Failed to query realtime journey")
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
				journeyReduced, err = marshalWithSheriffView(c, journey.RealtimeJourney, []string{"basic", "detailed"}, sheriffViews{
					"web": {"web-journey-realtime"},
				})
			}
		} else {
			applyJourneyServiceStopNameOverrides(journey)

			transforms.Transform(journey.Service, 1)
			transforms.Transform(journey.DetailedRailInformation, 1)

			journeyReduced, err = marshalWithSheriffView(c, journey, []string{"basic", "detailed"}, sheriffViews{
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
	if journey == nil {
		return
	}

	for _, pathItem := range journey.Path {
		if pathItem == nil {
			continue
		}

		pathItem.OriginStop.UpdateNameFromServiceOverrides(journey.Service)
		pathItem.DestinationStop.UpdateNameFromServiceOverrides(journey.Service)
	}
}
