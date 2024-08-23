package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/transforms"
)

func JourneysRouter(router fiber.Router) {
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
		journey.GetRealtimeJourney()

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
