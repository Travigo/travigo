package identifiers

import (
	"errors"
	"fmt"
	"strings"

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

	formatedStopID := fmt.Sprintf("%s-stop-%s", linkedDataset, stopID)

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

	formatedServiceID := fmt.Sprintf("%s-service-%s", linkedDataset, routeID)

	servicesQuery := bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": formatedServiceID},
			bson.M{"otheridentifiers": formatedServiceID},
		},
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

	journeysQuery := bson.M{
		"otheridentifiers.GTFS-TripID": tripID,
		"datasource.datasetid":         linkedDataset,
	}

	if strings.Contains(linkedDataset, "*") {
		journeysQuery["datasource.datasetid"] = bson.M{"$regex": linkedDataset}
	}

	potentialJourneys, err := findPrimaryIdentifiers(journeysCollection, journeysQuery, 2)
	if err != nil {
		return "", err
	}

	if len(potentialJourneys) == 0 {
		return "", errors.New("Could not find referenced trip")
	} else if len(potentialJourneys) == 1 {
		return potentialJourneys[0], nil
	} else {
		return "", errors.New("Could not find referenced trip")
	}
}
