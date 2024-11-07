package calculator

import (
	"context"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type OperatorsStats struct {
	Total int
}

func GetOperators() OperatorsStats {
	operatorsCollection := database.GetCollection("operators")
	numberOperators, _ := operatorsCollection.CountDocuments(context.Background(), bson.D{})

	return OperatorsStats{
		Total: int(numberOperators),
	}
}
