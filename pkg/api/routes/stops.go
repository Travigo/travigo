package routes

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"

	iso8601 "github.com/senseyeio/duration"
)

func StopsRouter(router fiber.Router) {
	router.Get("/", listStops)
	router.Get("/:identifier", getStop)
	router.Get("/:identifier/departure_board", getStopDepartureBoard)
}

func listStops(c *fiber.Ctx) error {
	boundsQuery := c.Query("bounds")

	if boundsQuery == "" {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "A filter must be applied to the request",
		})
	}

	boundsQuerySplit := strings.Split(boundsQuery, ",")
	if len(boundsQuerySplit) != 4 {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "Bounds must contain 4 co-ordinates",
		})
	}
	bottomLeftLon, _ := strconv.ParseFloat(boundsQuerySplit[0], 32)
	bottomLeftLat, _ := strconv.ParseFloat(boundsQuerySplit[1], 32)
	topRightLon, _ := strconv.ParseFloat(boundsQuerySplit[2], 32)
	topRightLat, _ := strconv.ParseFloat(boundsQuerySplit[3], 32)

	stops := []ctdf.Stop{}

	stopsCollection := database.GetCollection("stops")

	locationQuery := bson.M{"location": bson.M{"$geoWithin": bson.M{"$box": bson.A{bson.A{bottomLeftLon, bottomLeftLat}, bson.A{topRightLon, topRightLat}}}}}
	query := bson.M{"$and": bson.A{bson.M{"status": "active"}, locationQuery}}
	cursor, _ := stopsCollection.Find(context.Background(), query)

	for cursor.Next(context.TODO()) {
		var stop *ctdf.Stop
		err := cursor.Decode(&stop)
		if err != nil {
			log.Fatal(err)
		}

		stops = append(stops, *stop)
	}

	c.JSON(stops)
	return nil
}

func getStop(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	stopsCollection := database.GetCollection("stops")
	var stop *ctdf.Stop
	stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": identifier}).Decode(&stop)

	if stop == nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": "Could not find Stop matching Stop Identifier",
		})
	} else {
		stop.GetServices()
		return c.JSON(stop)
	}
}

func getStopDepartureBoard(c *fiber.Ctx) error {
	stopIdentifier := c.Params("identifier")

	stopsCollection := database.GetCollection("stops")
	var stop *ctdf.Stop
	stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": stopIdentifier}).Decode(&stop)

	if stop == nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": "Could not find Stop matching Stop Identifier",
		})
	}

	currentDateTime := time.Now()
	nextDayDuration, _ := iso8601.ParseISO8601("P1D")
	tomorrowDateTime := nextDayDuration.Shift(currentDateTime)
	tomorrowDateTime = time.Date(
		tomorrowDateTime.Year(), tomorrowDateTime.Month(), tomorrowDateTime.Day(), 0, 0, 0, 0, tomorrowDateTime.Location(),
	)

	journeys := []*ctdf.Journey{}

	journeysCollection := database.GetCollection("journeys")
	// departureTimeQuery := bson.M{"deperaturetime": bson.M{"$gte": currentTime}}
	// query := bson.M{"$and": bson.A{bson.M{"path.originstopref": stopIdentifier}, departureTimeQuery}}
	cursor, _ := journeysCollection.Find(context.Background(), bson.M{"path.originstopref": stopIdentifier})

	for cursor.Next(context.TODO()) {
		var journey ctdf.Journey
		err := cursor.Decode(&journey)
		if err != nil {
			log.Fatal(err)
		}

		journeys = append(journeys, &journey)
	}

	journeysTimetableToday := ctdf.GenerateTimetableFromJourneys(journeys, stopIdentifier, currentDateTime)
	journeysTimetableTomorrow := ctdf.GenerateTimetableFromJourneys(journeys, stopIdentifier, tomorrowDateTime)

	journeysTimetable := append(journeysTimetableToday, journeysTimetableTomorrow...)

	return c.JSON(journeysTimetable)
}
