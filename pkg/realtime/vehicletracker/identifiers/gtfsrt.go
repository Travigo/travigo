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

func (r *GTFSRT) IdentifyJourney() (string, error) {
	journeysCollection := database.GetCollection("journeys")

	tripID := r.IdentifyingInformation["TripID"]
	if tripID == "" {
		return "", errors.New("Could not find referenced trip")
	}

	var potentialJourneys []ctdf.Journey

	// TODO change this query to look at otheridentifiers
	cursor, _ := journeysCollection.Find(context.Background(), bson.M{"primaryidentifier": fmt.Sprintf("%s-journey-%s", "gb-bods-gtfs", tripID)})
	cursor.All(context.Background(), &potentialJourneys)

	if len(potentialJourneys) == 0 {
		return "", errors.New("Could not find related Journeys")
	} else if len(potentialJourneys) == 1 {
		return potentialJourneys[0].PrimaryIdentifier, nil
	} else {
		return "", errors.New("Could not narrow down to single Journey by time. Still many remaining")
	}
}
