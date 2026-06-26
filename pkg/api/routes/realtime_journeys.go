package routes

import (
	"context"
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
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
	boundsQuery, err := getLocationQuery(c)
	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	var realtimeJourneys []realtimeJourneyMinimised

	realtimeJourneyResults, err := realtimestore.FindActiveWithinBounds(context.Background(), boundsQuery)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query realtime journeys")
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	for _, realtimeJourney := range realtimeJourneyResults {
		if realtimeJourney.IsActive() {
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

	realtimeJourney, err := realtimestore.FindByIdentifier(context.Background(), identifier)
	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		return c.JSON(realtimeJourney)
	}
}
