package routes

import (
	"github.com/britbus/britbus/pkg/api/stats"
	"github.com/gofiber/fiber/v2"
)

func Stats(c *fiber.Ctx) error {
	return c.JSON(stats.CurrentRecordsStats)
}
