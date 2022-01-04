package routes

import (
	"context"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func StopsRouter(router fiber.Router) {
	router.Get("/", listStops)
	router.Get("/:identifier", getStop)
}

func listStops(c *fiber.Ctx) error {
	c.SendString("NOT IMPLEMENTED")
	return nil
}

func getStop(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	stopsCollection := database.GetCollection("stops")
	var stop *ctdf.Stop
	stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": identifier}).Decode(&stop)

	if stop == nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": "Could not find Stop matching Stop Identifier",
		})
	} else {
		return c.JSON(stop)
	}
}
