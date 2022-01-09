package routes

import (
	"context"
	"log"
	"strconv"
	"strings"

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

	//{location: { $geoWithin: { $box:  [ [ 0.002753078006207943, 52.12346747820989 ], [ 0.36976510193198925, 52.24976035425812 ] ] } }}
	query := bson.M{"location": bson.M{"$geoWithin": bson.M{"$box": bson.A{bson.A{bottomLeftLon, bottomLeftLat}, bson.A{topRightLon, topRightLat}}}}}
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
