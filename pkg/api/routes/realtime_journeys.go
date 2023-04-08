package routes

import (
	"context"
	"encoding/json"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/britbus/britbus/pkg/database"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
)

func RealtimeJourneysRouter(router fiber.Router) {
	router.Get("/", listRealtimeJourney)
	router.Get("/:identifier", getRealtimeJourney)
}

type realtimeJourneyMinimised struct {
	Journey struct {
		PrimaryIdentifier string

		Service *struct {
			// PrimaryIdentifier string `groups:"basic"`
			ServiceName string
		}

		Operator *struct {
			// PrimaryIdentifier string `groups:"basic"`
			PrimaryName string
		}

		DestinationDisplay string
	}

	VehicleLocation ctdf.Location
	VehicleBearing  float64
}

// TODO this should be using Sherrif instead of this dodgy json marhsall unmarshall
func newRealtimeJourneyMinimised(realtimeJourney *ctdf.RealtimeJourney) realtimeJourneyMinimised {
	realtimeJourneyMinimised := realtimeJourneyMinimised{}
	bytes, _ := json.Marshal(realtimeJourney)
	json.Unmarshal(bytes, &realtimeJourneyMinimised)

	return realtimeJourneyMinimised
}

func listRealtimeJourney(c *fiber.Ctx) error {
	boundsQuery, err := getBoundsQuery(c)
	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	realtimeJourneys := []realtimeJourneyMinimised{}

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()

	cursor, _ := realtimeJourneysCollection.Find(context.Background(),
		bson.M{"$and": bson.A{bson.M{"vehiclelocation": boundsQuery}, bson.M{
			"modificationdatetime": bson.M{"$gt": realtimeActiveCutoffDate},
		}}},
	)

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

			realtimeJourneys = append(realtimeJourneys, newRealtimeJourneyMinimised(realtimeJourney))
		}
	}

	c.JSON(realtimeJourneys)
	return nil
}

func getRealtimeJourney(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var realtimeJourney *ctdf.RealtimeJourney
	realtimeJourney, err := dataaggregator.Lookup[*ctdf.RealtimeJourney](query.RealtimeJourney{
		PrimaryIdentifier: identifier,
	})

	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		return c.JSON(realtimeJourney)
	}
}
