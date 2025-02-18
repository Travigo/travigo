package calculator

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type ServiceAlertsStats struct {
	Total    int
	Active   int
	Inactive int

	Datasets   map[string]int
	Providers  map[string]int
	Countries  map[string]int
	AlertTypes map[string]int
}

func GetServiceAlerts() ServiceAlertsStats {
	numberActiveAlerts := 0
	numberInactiveAlerts := 0
	collection := database.GetCollection("service_alerts")

	now := time.Now()

	cursor, _ := collection.Find(context.Background(), bson.M{})
	for cursor.Next(context.Background()) {
		var serviceAlert ctdf.ServiceAlert
		err := cursor.Decode(&serviceAlert)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode ServiceAlert")
		}

		if serviceAlert.IsValid(now) {
			numberActiveAlerts += 1
		} else {
			numberInactiveAlerts += 1
		}
	}

	datasources := CountAggregate(collection, "$datasource.datasetid")
	providers := CountAggregate(collection, "$datasource.providerid")
	alertTypes := CountAggregate(collection, "$alerttype")

	return ServiceAlertsStats{
		Total:    numberActiveAlerts + numberInactiveAlerts,
		Active:   numberActiveAlerts,
		Inactive: numberInactiveAlerts,

		Datasets:   datasources,
		Providers:  providers,
		Countries:  CountCountries(datasources),
		AlertTypes: alertTypes,
	}
}
