package identifiers

import (
	"context"
	"errors"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type GTFSRT struct {
	IdentifyingInformation map[string]string
}

func (r *GTFSRT) IdentifyStop() (string, error) {
	stopsCollection := database.GetCollection("stops")

	stopID := r.IdentifyingInformation["StopID"]
	if stopID == "" {
		return "", errors.New("Missing field StopID")
	}

	linkedDataset := r.IdentifyingInformation["LinkedDataset"]
	if linkedDataset == "" {
		return "", errors.New("Missing field linkedDataset")
	}

	var potentialStops []ctdf.Stop

	formatedStopID := fmt.Sprintf("%s-stop-%s", linkedDataset, stopID)

	cursor, _ := stopsCollection.Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": formatedStopID},
			bson.M{"otheridentifiers": formatedStopID},
		},
		"datasource.datasetid": linkedDataset,
	})
	cursor.All(context.Background(), &potentialStops)

	if len(potentialStops) == 0 {
		return "", errors.New("Could not find referenced stop")
	} else if len(potentialStops) == 1 {
		return potentialStops[0].PrimaryIdentifier, nil
	} else {
		return "", errors.New("Could not find referenced stop")
	}
}

func (r *GTFSRT) IdentifyService() (string, error) {
	servicesCollection := database.GetCollection("services")

	routeID := r.IdentifyingInformation["RouteID"]
	if routeID == "" {
		return "", errors.New("Missing field RouteID")
	}

	linkedDataset := r.IdentifyingInformation["LinkedDataset"]
	if linkedDataset == "" {
		return "", errors.New("Missing field linkedDataset")
	}

	var potentialServices []ctdf.Stop

	formatedServiceID := fmt.Sprintf("%s-service-%s", linkedDataset, routeID)

	cursor, _ := servicesCollection.Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": formatedServiceID},
			bson.M{"otheridentifiers": formatedServiceID},
		},
		"datasource.datasetid": linkedDataset,
	})
	cursor.All(context.Background(), &potentialServices)

	if len(potentialServices) == 0 {
		return "", errors.New("Could not find referenced service")
	} else if len(potentialServices) == 1 {
		return potentialServices[0].PrimaryIdentifier, nil
	} else {
		return "", errors.New("Could not find referenced service")
	}
}

func (r *GTFSRT) IdentifyJourney() (string, error) {
	journeysCollection := database.GetCollection("journeys")

	tripID := r.IdentifyingInformation["TripID"]
	if tripID == "" {
		return "", errors.New("Missing field tripid")
	}

	linkedDataset := r.IdentifyingInformation["LinkedDataset"]
	if linkedDataset == "" {
		return "", errors.New("Missing field linkedDataset")
	}

	var potentialJourneys []ctdf.Journey

	cursor, _ := journeysCollection.Find(context.Background(), bson.M{
		"otheridentifiers.GTFS-TripID": tripID,
		"datasource.datasetid":         linkedDataset,
	})
	cursor.All(context.Background(), &potentialJourneys)

	if len(potentialJourneys) == 0 {
		return "", errors.New("Could not find referenced trip")
	} else if len(potentialJourneys) == 1 {
		return potentialJourneys[0].PrimaryIdentifier, nil
	} else {
		return "", errors.New("Could not find referenced trip")
	}
}
