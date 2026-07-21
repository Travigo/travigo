package tflarrivals

import (
	"context"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type TfLLine struct {
	ModeID        string
	LineID        string
	LineName      string
	TransportType ctdf.TransportType

	OrderedLineRoutes    []OrderedLineRoute
	StopOccurrenceLimits map[string]int

	Service *ctdf.Service
}

func (l *TfLLine) GetService() {
	collection := database.GetCollection("services")
	query := bson.M{"operatorref": "gb-noc-TFLO", "servicename": l.LineName, "transporttype": l.TransportType}
	collection.FindOne(context.Background(), query).Decode(&l.Service)
}
