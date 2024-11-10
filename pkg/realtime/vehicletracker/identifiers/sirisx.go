package identifiers

import (
	"context"
	"errors"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type SiriSX struct {
	IdentifyingInformation map[string]string
}

func (r *SiriSX) IdentifyStop() (string, error) {
	stopsCollection := database.GetCollection("stops")

	stopID := r.IdentifyingInformation["StopPointRef"]
	if stopID == "" {
		return "", errors.New("Missing field StopPointRef")
	}

	linkedDataset := r.IdentifyingInformation["LinkedDataset"]
	if linkedDataset == "" {
		return "", errors.New("Missing field linkedDataset")
	}

	var potentialStops []ctdf.Stop

	formatedStopID := fmt.Sprintf("gb-atco-%s", stopID)

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

func (r *SiriSX) IdentifyService() (string, error) {
	servicesCollection := database.GetCollection("services")

	lineRef := r.IdentifyingInformation["LineRef"]
	if lineRef == "" {
		return "", errors.New("Missing field LineRef")
	}

	operatorRef := r.IdentifyingInformation["OperatorRef"]
	if operatorRef == "" {
		return "", errors.New("Missing field OperatorRef")
	}

	linkedDataset := r.IdentifyingInformation["LinkedDataset"]
	if linkedDataset == "" {
		return "", errors.New("Missing field linkedDataset")
	}

	var potentialServices []ctdf.Stop

	cursor, _ := servicesCollection.Find(context.Background(), bson.M{
		"servicename":          lineRef,
		"operatorref":          fmt.Sprintf("gb-noc-%s", operatorRef),
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

func (r *SiriSX) IdentifyJourney() (string, error) {
	return "", errors.New("Not supported")
}
