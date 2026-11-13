package identifiers

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs/nyct"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var errGTFSJourneyNotFound = errors.New("Could not find referenced trip")

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
	stopIdentifierFilter := any(formatedStopID)
	if strings.Contains(linkedDataset, "*") {
		stopIdentifierFilter = bson.M{"$regex": fmt.Sprintf("^%s-stop-%s$", datasetIDPattern(linkedDataset), regexp.QuoteMeta(stopID))}
	}

	stopsQuery := bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": stopIdentifierFilter},
			bson.M{"otheridentifiers": stopIdentifierFilter},
		},
		"datasource.datasetid": datasetIDFilter(linkedDataset),
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
	serviceIdentifierFilter := any(formatedServiceID)
	if strings.Contains(linkedDataset, "*") {
		serviceIdentifierFilter = bson.M{"$regex": fmt.Sprintf("^%s-service-%s$", datasetIDPattern(linkedDataset), regexp.QuoteMeta(routeID))}
	}

	servicesQuery := bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": serviceIdentifierFilter},
			bson.M{"otheridentifiers": serviceIdentifierFilter},
		},
		"datasource.datasetid": datasetIDFilter(linkedDataset),
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
	return r.identifyJourney(telemetry.GTFSJourneyQuery)
}

func (r *GTFSRT) identifyJourney(queryStage telemetry.Stage) (string, error) {
	tripID := r.IdentifyingInformation["TripID"]
	if tripID == "" {
		return "", errors.New("Missing field tripid")
	}

	linkedDataset := r.IdentifyingInformation["LinkedDataset"]
	if linkedDataset == "" {
		return "", errors.New("Missing field linkedDataset")
	}

	if linkedDataset == nyct.SubwayDataset {
		return nyct.IdentifyJourneyWithQueryObserver(tripID, r.IdentifyingInformation["TripStartDate"], func(d time.Duration) { telemetry.Observe(queryStage, d) })
	}

	journeysCollection := database.GetCollection("journeys")
	journeysQuery := bson.M{
		"otheridentifiers.GTFS-TripID": tripID,
		"datasource.datasetid":         datasetIDFilter(linkedDataset),
	}

	// Frequency-expanded journeys retain the source trip_id but expose their
	// concrete GTFS frequency start time as an additional identifier. Prefer it
	// when GTFS-RT supplies TripDescriptor.start_time; fall back to the legacy
	// trip_id-only lookup for ordinary trips and older feeds.
	if tripStartTime := r.IdentifyingInformation["TripStartTime"]; tripStartTime != "" {
		frequencyQuery := make(bson.M, len(journeysQuery)+1)
		for key, value := range journeysQuery {
			frequencyQuery[key] = value
		}
		frequencyQuery["otheridentifiers.GTFS-TripStartTime"] = tripStartTime

		potentialJourneys, err := findGTFSJourneyIdentifiers(journeysCollection, frequencyQuery, queryStage)
		if err != nil {
			return "", err
		}
		if len(potentialJourneys) == 1 {
			return potentialJourneys[0], nil
		}
		if len(potentialJourneys) > 1 {
			return "", errGTFSJourneyNotFound
		}
	}

	potentialJourneys, err := findGTFSJourneyIdentifiers(journeysCollection, journeysQuery, queryStage)
	if err != nil {
		return "", err
	}

	if len(potentialJourneys) != 1 {
		return "", errGTFSJourneyNotFound
	}
	return potentialJourneys[0], nil
}

func findGTFSJourneyIdentifiers(collection *mongo.Collection, query bson.M, stage telemetry.Stage) ([]string, error) {
	started := time.Now()
	defer func() { telemetry.Observe(stage, time.Since(started)) }()
	return findPrimaryIdentifiers(collection, query, 2)
}

func datasetIDFilter(linkedDataset string) any {
	if !strings.Contains(linkedDataset, "*") {
		return linkedDataset
	}
	return bson.M{"$regex": "^" + datasetIDPattern(linkedDataset) + "$"}
}

func datasetIDPattern(linkedDataset string) string {
	// Treat '*' in LinkedDataset as a glob while escaping all other regex metacharacters.
	return strings.ReplaceAll(regexp.QuoteMeta(linkedDataset), `\*`, `.*`)
}
