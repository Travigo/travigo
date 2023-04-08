package routes

import (
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
)

func JourneysRouter(router fiber.Router) {
	router.Get("/:identifier", getJourney)
}

func getJourney(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

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
		journey.GetRealtimeJourney(time.Now().Format("2006-01-02"))

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
