package routes

import (
	"github.com/britbus/britbus/pkg/stats"
	"github.com/gofiber/fiber/v2"
)

func IdentificationRateRouter(router fiber.Router) {
	router.Get("/", getIdentificationRate)
}

func getIdentificationRate(c *fiber.Ctx) error {
	rateStats := stats.GetIdentificationRateStats()

	return c.JSON(rateStats)
}
