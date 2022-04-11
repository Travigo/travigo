package routes

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func getBoundsQuery(c *fiber.Ctx) (bson.M, error) {
	bounds := c.Query("bounds")

	if bounds == "" {
		return nil, errors.New("A filter must be applied to the request")
	}

	boundsSplit := strings.Split(bounds, ",")
	if len(boundsSplit) != 4 {
		return nil, errors.New("Bounds must contain 4 co-ordinates")
	}

	bottomLeftLon, _ := strconv.ParseFloat(boundsSplit[0], 32)
	bottomLeftLat, _ := strconv.ParseFloat(boundsSplit[1], 32)
	topRightLon, _ := strconv.ParseFloat(boundsSplit[2], 32)
	topRightLat, _ := strconv.ParseFloat(boundsSplit[3], 32)

	return bson.M{"$geoWithin": bson.M{"$box": bson.A{bson.A{bottomLeftLon, bottomLeftLat}, bson.A{topRightLon, topRightLat}}}}, nil
}
