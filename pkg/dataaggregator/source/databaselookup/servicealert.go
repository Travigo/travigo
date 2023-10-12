package databaselookup

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
)

func (s Source) ServiceAlertsForMatchingIdentifiersQuery(q query.ServiceAlertsForMatchingIdentifiers) ([]*ctdf.ServiceAlert, error) {
	collection := database.GetCollection("service_alerts")
	var serviceAlerts []*ctdf.ServiceAlert

	now := time.Now()

	cursor, _ := collection.Find(context.Background(), q.ToBson())
	for cursor.Next(context.Background()) {
		var serviceAlert ctdf.ServiceAlert
		err := cursor.Decode(&serviceAlert)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode ServiceAlert")
		}

		if serviceAlert.IsValid(now) {
			serviceAlerts = append(serviceAlerts, &serviceAlert)
		}
	}

	return serviceAlerts, nil
}
