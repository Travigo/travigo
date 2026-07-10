package routes

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
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

		var journeyReduced interface{}

		if realtimeOnly {
			if journey.RealtimeJourney == nil {
				journeyReduced = nil
			} else {
				journeyReduced, err = sheriff.Marshal(&sheriff.Options{
					Groups: []string{"basic", "detailed"},
				}, journey.RealtimeJourney)
			}
		} else {
			// for _, pathItem := range journey.Path {
			// 	pathItem.OriginStop.UpdateNameFromServiceOverrides(journey.Service)
			// 	pathItem.DestinationStop.UpdateNameFromServiceOverrides(journey.Service)
			// }

			transforms.Transform(journey.Service, 1)
			transforms.Transform(journey.DetailedRailInformation, 1)

			journeyReduced, err = sheriff.Marshal(&sheriff.Options{
				Groups: []string{"basic", "detailed"},
			}, journey)
		}

		if err != nil {
			c.SendStatus(fiber.StatusInternalServerError)
			return c.JSON(fiber.Map{
				"error": "Sherrif could not reduce Journey",
			})
		}

		return c.JSON(journeyReduced)
	}
}
