package routes

import (
	"context"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func StopGroupsRouter(router fiber.Router) {
	router.Get("/:identifier", getStopGroup)
}

func getStopGroup(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	stopGroupsCollection := database.GetCollection("stop_groups")
	var stopGroup *ctdf.StopGroup
	stopGroupsCollection.FindOne(context.Background(), bson.M{"identifier": identifier}).Decode(&stopGroup)

	if stopGroup == nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": "Could not find Stop Group matching Identifier",
		})
	} else {
		stopGroup.GetStops()
		return c.JSON(stopGroup)
	}
}
