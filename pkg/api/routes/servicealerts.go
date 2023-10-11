package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
)

func ServiceAlertRouter(router fiber.Router) {
	router.Get("/matching/:identifier", getMatchingIdentifierServiceAlerts)
	router.Get("/stop/:identifier", getStopServiceAlerts)
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

func getStopServiceAlerts(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	// First get the stop
	var stop *ctdf.Stop
	stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		PrimaryIdentifier: identifier,
	})

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Get the services for that stop
	var services []*ctdf.Service
	services, err = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
		Stop: stop,
	})

	// setup query for disruptions
	matchingIdentifiers := []string{
		identifier,
	}

	for _, service := range services {
		matchingIdentifiers = append(matchingIdentifiers, service.PrimaryIdentifier)
	}

	var serviceAlerts []*ctdf.ServiceAlert
	serviceAlerts, err = dataaggregator.Lookup[[]*ctdf.ServiceAlert](query.ServiceAlertsForMatchingIdentifiers{
		MatchingIdentifiers: matchingIdentifiers,
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
