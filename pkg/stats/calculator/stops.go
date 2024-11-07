package calculator

import (
	"context"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type StopsStats struct {
	Total int
}

func GetStops() StopsStats {
	stopsCollection := database.GetCollection("stops")
	numberStops, _ := stopsCollection.CountDocuments(context.Background(), bson.D{})

	return StopsStats{
		Total: int(numberStops),
	}
}
