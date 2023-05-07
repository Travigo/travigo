package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
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
