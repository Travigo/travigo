package routes

import (
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/gofiber/fiber/v2"
)

func ServicesRouter(router fiber.Router) {
	router.Get("/:identifier", getService)
}

func getService(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var service *ctdf.Service
	service, err := dataaggregator.Lookup[*ctdf.Service](query.Service{
		PrimaryIdentifier: identifier,
	})

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		return c.JSON(service)
	}
}
