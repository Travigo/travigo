package identifiers

import (
	"errors"
	"fmt"
	"strings"

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

	formatedStopID := fmt.Sprintf("gb-atco-%s", stopID)

	stopsQuery := bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": formatedStopID},
			bson.M{"otheridentifiers": formatedStopID},
		},
		"datasource.datasetid": linkedDataset,
	}

	if strings.Contains(linkedDataset, "*") {
		stopsQuery["datasource.datasetid"] = bson.M{"$regex": linkedDataset}
	}

	potentialStops, err := findPrimaryIdentifiers(stopsCollection, stopsQuery, 2)
	if err != nil {
		return "", err
	}

	if len(potentialStops) == 0 {
		return "", errors.New("Could not find referenced stop")
	} else if len(potentialStops) == 1 {
		return potentialStops[0], nil
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

	servicesQuery := bson.M{
		"servicename":          lineRef,
		"operatorref":          fmt.Sprintf("gb-noc-%s", operatorRef),
		"datasource.datasetid": linkedDataset,
	}

	if strings.Contains(linkedDataset, "*") {
		servicesQuery["datasource.datasetid"] = bson.M{"$regex": linkedDataset}
	}

	potentialServices, err := findPrimaryIdentifiers(servicesCollection, servicesQuery, 2)
	if err != nil {
		return "", err
	}

	if len(potentialServices) == 0 {
		return "", errors.New("Could not find referenced service")
	} else if len(potentialServices) == 1 {
		return potentialServices[0], nil
	} else {
		return "", errors.New("Could not find referenced service")
	}
}

func (r *SiriSX) IdentifyJourney() (string, error) {
	return "", errors.New("Not supported")
}
