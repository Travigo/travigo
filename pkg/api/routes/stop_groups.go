package routes

import (
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/gofiber/fiber/v2"
)

func StopGroupsRouter(router fiber.Router) {
	router.Get("/:identifier", getStopGroup)
}

func getStopGroup(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var stopGroup *ctdf.StopGroup
	stopGroup, err := dataaggregator.Lookup[*ctdf.StopGroup](query.StopGroup{
		PrimaryIdentifier: identifier,
	})

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		stopGroup.GetStops()
		return c.JSON(stopGroup)
	}
}
