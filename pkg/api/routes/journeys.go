package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
)

func JourneysRouter(router fiber.Router) {
	router.Get("/:identifier", getJourney)
}

func getJourney(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var journey *ctdf.Journey
	journey, err := dataaggregator.Lookup[*ctdf.Journey](ctdf.QueryJourney{
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

		for _, pathItem := range journey.Path {
			pathItem.OriginStop.UpdateNameFromServiceOverrides(journey.Service)
			pathItem.DestinationStop.UpdateNameFromServiceOverrides(journey.Service)
		}

		journeyReduced, err := sheriff.Marshal(&sheriff.Options{
			Groups: []string{"basic", "detailed"},
		}, journey)

		if err != nil {
			c.SendStatus(fiber.StatusInternalServerError)
			return c.JSON(fiber.Map{
				"error": "Sherrif could not reduce Journey",
			})
		}

		return c.JSON(journeyReduced)
	}
}
