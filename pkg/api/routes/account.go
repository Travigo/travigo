package routes

import (
	"github.com/gofiber/fiber/v2"
)

func AccountRouter(router fiber.Router) {
	router.Post("/notificationtoken", postNotificationToken)
}

func postNotificationToken(c *fiber.Ctx) error {
	var requestBody struct {
		Token string
	}

	c.BodyParser(&requestBody)

	return c.JSON(fiber.Map{
		"test": requestBody,
		"user": c.Locals("account_userid"),
	})
}
