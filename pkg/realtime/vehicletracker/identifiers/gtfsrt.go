package identifiers

import (
	"context"
	"errors"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type GTFSRT struct {
	IdentifyingInformation map[string]string
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
