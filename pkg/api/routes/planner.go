package routes

import (
	"sort"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
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
		Identifier: originIdentifier,
	})
	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	var destinationStop *ctdf.Stop
	destinationStop, err = dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		Identifier: destinationIdentifier,
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
	var journeyPlans *ctdf.JourneyPlanResults

	journeyPlans, err = dataaggregator.Lookup[*ctdf.JourneyPlanResults](query.JourneyPlan{
		OriginStop:      originStop,
		DestinationStop: destinationStop,
		Count:           count,
		StartDateTime:   startDateTime,
	})

	// Sort departures by DepartureBoard time
	sort.Slice(journeyPlans.JourneyPlans, func(i, j int) bool {
		return journeyPlans.JourneyPlans[i].StartTime.Before(journeyPlans.JourneyPlans[j].StartTime)
	})

	// Once sorted cut off any records higher than our max count
	if len(journeyPlans.JourneyPlans) > count {
		journeyPlans.JourneyPlans = journeyPlans.JourneyPlans[:count]
	}

	reducedJourneyPlans, _ := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"basic"},
	}, journeyPlans)

	return c.JSON(reducedJourneyPlans)
}
