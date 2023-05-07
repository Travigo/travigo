package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/api/stats"
)

func Stats(c *fiber.Ctx) error {
	return c.JSON(stats.CurrentRecordsStats)
}
