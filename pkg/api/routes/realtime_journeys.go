package routes

import (
	"context"

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

func newRealtimeJourneyMinimised(realtimeJourney *ctdf.RealtimeJourney) realtimeJourneyMinimised {
	minimised := realtimeJourneyMinimised{}
	if realtimeJourney == nil {
		return minimised
	}

	minimised.VehicleLocation = realtimeJourney.VehicleLocation
	minimised.VehicleBearing = realtimeJourney.VehicleBearing
	if realtimeJourney.Journey == nil {
		return minimised
	}

	minimised.Journey.PrimaryIdentifier = realtimeJourney.Journey.PrimaryIdentifier
	minimised.Journey.DestinationDisplay = realtimeJourney.Journey.DestinationDisplay
	if realtimeJourney.Journey.Service != nil {
		minimised.Journey.Service = &struct {
			ServiceName string
		}{ServiceName: realtimeJourney.Journey.Service.ServiceName}
	}
	if realtimeJourney.Journey.Operator != nil {
		minimised.Journey.Operator = &struct {
			PrimaryName string
		}{PrimaryName: realtimeJourney.Journey.Operator.PrimaryName}
	}

	return minimised
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
	populateRealtimeJourneyListReferences(realtimeJourneyResults)

	for _, realtimeJourney := range realtimeJourneyResults {
		if realtimeJourney.IsActive() && realtimeJourney.Journey != nil {
			realtimeJourneys = append(realtimeJourneys, newRealtimeJourneyMinimised(realtimeJourney))
		}
	}

	c.JSON(realtimeJourneys)
	return nil
}

func populateRealtimeJourneyListReferences(realtimeJourneys []*ctdf.RealtimeJourney) {
	operatorRefs := map[string]struct{}{}
	serviceRefs := map[string]struct{}{}
	for _, realtimeJourney := range realtimeJourneys {
		if realtimeJourney == nil || realtimeJourney.Journey == nil {
			continue
		}
		if realtimeJourney.Journey.Operator == nil && realtimeJourney.Journey.OperatorRef != "" {
			operatorRefs[realtimeJourney.Journey.OperatorRef] = struct{}{}
		}
		if realtimeJourney.Journey.Service == nil && realtimeJourney.Journey.ServiceRef != "" {
			serviceRefs[realtimeJourney.Journey.ServiceRef] = struct{}{}
		}
	}

	operatorsByID := loadOperatorsByReferences(operatorRefs)
	servicesByID := loadServicesByReferences(serviceRefs)
	for _, realtimeJourney := range realtimeJourneys {
		if realtimeJourney == nil || realtimeJourney.Journey == nil {
			continue
		}
		if realtimeJourney.Journey.Operator == nil {
			realtimeJourney.Journey.Operator = operatorsByID[realtimeJourney.Journey.OperatorRef]
		}
		if realtimeJourney.Journey.Service == nil {
			realtimeJourney.Journey.Service = servicesByID[realtimeJourney.Journey.ServiceRef]
		}
	}
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
		if realtimeJourney.Journey != nil {
			realtimeJourney.Journey.GetTracks()
		}
		return c.JSON(realtimeJourney)
	}
}
