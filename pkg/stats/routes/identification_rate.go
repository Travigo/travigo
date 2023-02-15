package routes

import (
	"strings"

	"github.com/britbus/britbus/pkg/stats"
	"github.com/gofiber/fiber/v2"
)

func IdentificationRateRouter(router fiber.Router) {
	router.Get("/", getIdentificationRate)
}

func getIdentificationRate(c *fiber.Ctx) error {
	operatorsListString := c.Query("operators", "")

	var operatorsList []string
	if operatorsListString != "" {
		operatorsList = strings.Split(operatorsListString, ",")
	}

	rateStats := stats.GetIdentificationRateStats(operatorsList)

	return c.JSON(rateStats)
}
