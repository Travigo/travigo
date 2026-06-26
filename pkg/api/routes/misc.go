package routes

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func getLocationQuery(c *fiber.Ctx) (bson.M, error) {
	bounds := c.Query("bounds")
	point := c.Query("point")

	if bounds != "" {
		boundsSplit := strings.Split(bounds, ",")
		if len(boundsSplit) != 4 {
			return nil, errors.New("Bounds must contain 4 co-ordinates")
		}

		bottomLeftLon, _ := strconv.ParseFloat(boundsSplit[0], 32)
		bottomLeftLat, _ := strconv.ParseFloat(boundsSplit[1], 32)
		topRightLon, _ := strconv.ParseFloat(boundsSplit[2], 32)
		topRightLat, _ := strconv.ParseFloat(boundsSplit[3], 32)

		return bson.M{
			"$geoWithin": bson.M{
				"$box": bson.A{
					bson.A{bottomLeftLon, bottomLeftLat},
					bson.A{topRightLon, topRightLat},
				},
			},
		}, nil
	} else if point != "" {
		pointSplit := strings.Split(point, ",")
		if len(pointSplit) != 2 {
			return nil, errors.New("Point must contain 2 co-ordinates")
		}

		lon, _ := strconv.ParseFloat(pointSplit[0], 32)
		lat, _ := strconv.ParseFloat(pointSplit[1], 32)

		return bson.M{
			"$near": bson.M{
				"$geometry": bson.M{
					"type":        "Point",
					"coordinates": bson.A{lon, lat},
				},
				"$maxDistance": 500,
			},
		}, nil
	}

	return nil, errors.New("A bounds or point filter must be applied to the request")
}
