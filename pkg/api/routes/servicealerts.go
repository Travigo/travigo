package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
)

func ServiceAlertRouter(router fiber.Router) {
	router.Get("/matching/:identifier", getMatchingIdentifierServiceAlerts)
}

func getMatchingIdentifierServiceAlerts(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var serviceAlerts []*ctdf.ServiceAlert
	serviceAlerts, err := dataaggregator.Lookup[[]*ctdf.ServiceAlert](query.ServiceAlertsForMatchingIdentifier{
		MatchingIdentifier: identifier,
	})

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		return c.JSON(serviceAlerts)
	}
}
