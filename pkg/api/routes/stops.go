package routes

import (
	"context"
	"regexp"

	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/naptan"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func StopsRouter(router fiber.Router) {
	router.Get("/", listStops)
	router.Get("/:stopID", getStop)
}

func listStops(c *fiber.Ctx) error {
	c.SendString("List stops")
	return nil
}

func getStop(c *fiber.Ctx) error {
	stopID := c.Params("stopID")
	atcoCodeRegex := regexp.MustCompile(`^[0-9]{3}[0-1][A-Za-z0-9]{1,8}$`)
	naptanCodeRegex := regexp.MustCompile(`^[0-9a-zA-Z]{5,9}$`)

	var searchField string

	if atcoCodeRegex.Match([]byte(stopID)) {
		searchField = "atcocode"
	} else if naptanCodeRegex.Match([]byte(stopID)) {
		searchField = "naptancode"
	} else {
		c.SendStatus(400)
		return c.JSON(fiber.Map{
			"error": "Invalid stopID Format. Must be a Atco or NaPTAN code",
		})
	}

	stopsCollection := database.GetCollection("stops")
	var stopPoint *naptan.StopPoint
	stopsCollection.FindOne(context.Background(), bson.M{searchField: stopID}).Decode(&stopPoint)

	if stopPoint == nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": "Could not find Stop matching stopID",
		})
	} else {
		return c.JSON(stopPoint)
	}
}
