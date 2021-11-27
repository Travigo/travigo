package routes

import (
	"context"
	"regexp"

	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/naptan"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func StopAreasRouter(router fiber.Router) {
	router.Get("/:stopAreaCode", getStopArea)
}

func getStopArea(c *fiber.Ctx) error {
	stopAreaCode := c.Params("stopAreaCode")
	stopAreaCodeRegex := regexp.MustCompile(`^[0-9]{3}[G0][A-Za-z0-9]{1,8}$`)

	if !stopAreaCodeRegex.Match([]byte(stopAreaCode)) {
		c.SendStatus(400)
		return c.JSON(fiber.Map{
			"error": "Invalid StopAreaCode Format.",
		})
	}

	stopAreasCollection := database.GetCollection("stopareas")
	var stopArea *naptan.StopArea
	stopAreasCollection.FindOne(context.Background(), bson.M{"stopareacode": stopAreaCode}).Decode(&stopArea)

	if stopArea == nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": "Could not find Stop Areea matching StopAreaCode",
		})
	} else {
		stopArea.GetStops()
		return c.JSON(stopArea)
	}
}
