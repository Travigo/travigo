package routes

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/transforms"
	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
)

func StopsRouter(router fiber.Router) {
	router.Get("/", listStops)
	router.Get("/:identifier", getStop)
	router.Get("/:identifier/departures", getStopDepartures)
}

func listStops(c *fiber.Ctx) error {
	boundsQuery, err := getBoundsQuery(c)
	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	stops := []ctdf.Stop{}

	stopsCollection := database.GetCollection("stops")

	query := bson.M{"location": boundsQuery}

	transportTypeFilter := c.Query("transport_type")
	if transportTypeFilter != "" {
		transportType := strings.Split(transportTypeFilter, ",")

		query = bson.M{
			"$and": bson.A{
				bson.M{"transporttypes": bson.M{"$in": transportType}},
				bson.M{"location": boundsQuery},
			},
		}
	}

	cursor, _ := stopsCollection.Find(context.Background(), query)

	for cursor.Next(context.TODO()) {
		var stop *ctdf.Stop
		err := cursor.Decode(&stop)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode Stop")
		}

		stops = append(stops, *stop)
	}

	c.JSON(stops)
	return nil
}

func getStop(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var stop *ctdf.Stop
	stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		PrimaryIdentifier: identifier,
	})

	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		stop.GetServices()

		transforms.Transform(stop, 3)

		return c.JSON(stop)
	}
}

func getStopDepartures(c *fiber.Ctx) error {
	stopIdentifier := c.Params("identifier")
	count, err := strconv.Atoi(c.Query("count", "25"))
	startDateTimeString := c.Query("datetime")

	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "Parameter count should be an integer",
		})
	}

	var stop *ctdf.Stop
	stop, err = dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		PrimaryIdentifier: stopIdentifier,
	})

	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

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

	var departureBoard []*ctdf.DepartureBoard

	departureBoard, err = dataaggregator.Lookup[[]*ctdf.DepartureBoard](query.DepartureBoard{
		Stop:          stop,
		Count:         count,
		StartDateTime: startDateTime,
	})

	departureBoardReduced, err := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"basic"},
	}, departureBoard)

	if err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": "Sherrif could not reduce departureBoard",
		})
	}

	return c.JSON(departureBoardReduced)
}
