package databaselookup

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
)

func (s Source) ServiceAlertsForMatchingIdentifierQuery(q query.ServiceAlertsForMatchingIdentifier) ([]*ctdf.ServiceAlert, error) {
	collection := database.GetCollection("service_alerts")
	var serviceAlerts []*ctdf.ServiceAlert

	cursor, _ := collection.Find(context.Background(), q.ToBson())
	if err := cursor.All(context.Background(), &serviceAlerts); err != nil {
		log.Error().Err(err).Msg("Failed to decode Service Alerts")
	}

	return serviceAlerts, nil
}

func (s Source) ServiceAlertsForMatchingIdentifiersQuery(q query.ServiceAlertsForMatchingIdentifiers) ([]*ctdf.ServiceAlert, error) {
	collection := database.GetCollection("service_alerts")
	var serviceAlerts []*ctdf.ServiceAlert

	cursor, _ := collection.Find(context.Background(), q.ToBson())
	if err := cursor.All(context.Background(), &serviceAlerts); err != nil {
		log.Error().Err(err).Msg("Failed to decode Service Alerts")
	}

	return serviceAlerts, nil
}
