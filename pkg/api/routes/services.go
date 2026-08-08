package routes

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/transforms"
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
		transforms.Transform(service, 2)

		if c.Query("view") == "web" {
			reduced, marshalErr := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-service-detail"}}, service)
			if marshalErr != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": marshalErr.Error()})
			}
			return c.JSON(reduced)
		}
		if c.Query("view") != "" {
			return sheriffViewError(c, fmt.Errorf("unsupported view %q", c.Query("view")))
		}
		return c.JSON(service)
	}
}
