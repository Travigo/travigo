package routes

import (
	"context"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
)

func RealtimeJourneysRouter(router fiber.Router) {
	router.Get("/", listRealtimeJourney)
	router.Get("/:identifier", getRealtimeJourney)
}

func listRealtimeJourney(c *fiber.Ctx) error {
	boundsQuery, err := getBoundsQuery(c)
	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	realtimeJourneys := []ctdf.RealtimeJourney{}

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	cursor, _ := realtimeJourneysCollection.Find(context.Background(), bson.M{"vehiclelocation": boundsQuery})

	for cursor.Next(context.TODO()) {
		var realtimeJourney *ctdf.RealtimeJourney
		err := cursor.Decode(&realtimeJourney)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode Stop")
		}

		if realtimeJourney.IsActive() {
			realtimeJourney.GetReferences()
			realtimeJourney.Journey.GetService()
			realtimeJourney.Journey.GetOperator()

			realtimeJourneys = append(realtimeJourneys, *realtimeJourney)
		}
	}

	c.JSON(realtimeJourneys)
	return nil
}

func getRealtimeJourney(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	realtimeJourneyCollection := database.GetCollection("realtime_journeys")
	var realtimeJourney *ctdf.RealtimeJourney
	realtimeJourneyCollection.FindOne(context.Background(), bson.M{"primaryidentifier": identifier}).Decode(&realtimeJourney)

	if realtimeJourney == nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": "Could not find Realtime Journey matching Identifier",
		})
	} else {
		return c.JSON(realtimeJourney)
	}
}
