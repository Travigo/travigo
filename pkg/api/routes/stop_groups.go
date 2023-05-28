package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
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
