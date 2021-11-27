package routes

import "github.com/gofiber/fiber/v2"

func APIVersion(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"version": "v0.1",
	})
}
