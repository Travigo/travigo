package routes

import (
	"context"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func JourneysRouter(router fiber.Router) {
	router.Get("/", listJourneys)
	router.Get("/:identifier", getJourney)
}

func listJourneys(c *fiber.Ctx) error {
	c.SendString("NOT IMPLEMENTED")
	return nil
}

func getJourney(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	journeysCollection := database.GetCollection("journeys")
	var journey *ctdf.Journey
	journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": identifier}).Decode(&journey)

	if journey == nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": "Could not find Journey matching Journey Identifier",
		})
	} else {
		journey.GetReferences()
		journey.GetDeepReferences()
		return c.JSON(journey)
	}
}
