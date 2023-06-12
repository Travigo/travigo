package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"sort"
	"strconv"
	"time"
)

func PlannerRouter(router fiber.Router) {
	router.Get("/:origin/:destination", getPlanBetweenStops)
}

func getPlanBetweenStops(c *fiber.Ctx) error {
	originIdentifier := c.Params("origin")
	destinationIdentifier := c.Params("destination")

	count, err := strconv.Atoi(c.Query("count", "25"))
	startDateTimeString := c.Query("datetime")

	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "Parameter count should be an integer",
		})
	}

	// Get stops
	var originStop *ctdf.Stop
	originStop, err = dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		PrimaryIdentifier: originIdentifier,
	})
	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	var destinationStop *ctdf.Stop
	destinationStop, err = dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		PrimaryIdentifier: destinationIdentifier,
	})
	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Get start time
	var startDateTime time.Time
	if startDateTimeString == "" {
		startDateTime = time.Now()
	} else {
		startDateTime, err = time.Parse(time.RFC3339, startDateTimeString)

		if err != nil {
			c.SendStatus(fiber.StatusBadRequest)
			return c.JSON(fiber.Map{
				"error":    "Parameter datetime should be an RFS3339/ISO8601 datetime",
				"detailed": err,
			})
		}
	}

	// Do the lookup
	var journeyPlans []ctdf.JourneyPlan

	journeyPlans, err = dataaggregator.Lookup[[]ctdf.JourneyPlan](query.JourneyPlan{
		OriginStop:      originStop,
		DestinationStop: destinationStop,
		Count:           count,
		StartDateTime:   startDateTime,
	})

	// Sort departures by DepartureBoard time
	sort.Slice(journeyPlans, func(i, j int) bool {
		return journeyPlans[i].StartTime.Before(journeyPlans[j].StartTime)
	})

	// Once sorted cut off any records higher than our max count
	if len(journeyPlans) > count {
		journeyPlans = journeyPlans[:count]
	}

	return c.JSON(journeyPlans)
}
