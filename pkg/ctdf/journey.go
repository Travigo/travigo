package ctdf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const XSDDateTimeFormat = "2006-01-02T15:04:05-07:00"
const XSDDateTimeWithFractionalFormat = "2006-01-02T15:04:05.999999-07:00"

type Journey struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"detailed"`

	ServiceRef string   `groups:"internal"`
	Service    *Service `groups:"basic" json:",omitempty" bson:"-"`

	OperatorRef string    `groups:"internal"`
	Operator    *Operator `groups:"basic" json:",omitempty" bson:"-"`

	Direction          string    `groups:"detailed"`
	DepartureTime      time.Time `groups:"basic"`
	DestinationDisplay string    `groups:"basic"`

	Availability *Availability `groups:"internal"`

	Path []*JourneyPathItem `groups:"detailed"`

	RealtimeJourney *RealtimeJourney `groups:"basic"`
}

func (j *Journey) GetReferences() {
	j.GetOperator()
	j.GetService()
}
func (j *Journey) GetOperator() {
	operatorsCollection := database.GetCollection("operators")
	query := bson.M{"$or": bson.A{bson.M{"primaryidentifier": j.OperatorRef}, bson.M{"otheridentifiers": j.OperatorRef}}}
	operatorsCollection.FindOne(context.Background(), query).Decode(&j.Operator)
}
func (j *Journey) GetService() {
	servicesCollection := database.GetCollection("services")
	servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": j.ServiceRef}).Decode(&j.Service)
}
func (j *Journey) GetDeepReferences() {
	for _, path := range j.Path {
		path.GetReferences()
	}
}
func (j *Journey) GetRealtimeJourney(timeframe string) {
	realtimeJourneyIdentifier := fmt.Sprintf(RealtimeJourneyIDFormat, timeframe, j.PrimaryIdentifier)
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	var realtimeJourney *RealtimeJourney
	realtimeJourneysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": realtimeJourneyIdentifier}).Decode(&realtimeJourney)

	if realtimeJourney != nil && realtimeJourney.IsActive() {
		j.RealtimeJourney = realtimeJourney
	}
}
func (j Journey) MarshalBinary() ([]byte, error) {
	return json.Marshal(j)
}

// The CTDF abstraction fails here are we only use siri-vm identifyinginformation
//  currently no other kind so is fine for now (TODO)
func IdentifyJourney(identifyingInformation map[string]string) (*Journey, error) {
	currentTime := time.Now()

	// Get the directly referenced Operator
	var referencedOperator *Operator
	operatorRef := identifyingInformation["OperatorRef"]
	operatorsCollection := database.GetCollection("operators")
	query := bson.M{"$or": bson.A{bson.M{"primaryidentifier": operatorRef}, bson.M{"otheridentifiers": operatorRef}}}
	operatorsCollection.FindOne(context.Background(), query).Decode(&referencedOperator)

	if referencedOperator == nil {
		return nil, errors.New("Could not find referenced Operator")
	}
	referencedOperator.GetOperatorGroup()

	// Get all potential Operators that belong in the Operator group
	// This is because *some* operator groups have incorrect operator IDs for a service
	var operators []string
	if referencedOperator.OperatorGroup == nil {
		operators = append(operators, referencedOperator.OtherIdentifiers...)
	} else {
		referencedOperator.OperatorGroup.GetOperators()
		for _, operator := range referencedOperator.OperatorGroup.Operators {
			operators = append(operators, operator.OtherIdentifiers...)
		}
	}

	// Get the relevant Services
	var services []string
	serviceName := identifyingInformation["ServiceNameRef"]
	servicesCollection := database.GetCollection("services")

	query = bson.M{
		"$and": bson.A{bson.M{"servicename": serviceName},
			bson.M{"operatorref": bson.M{"$in": operators}},
		},
	}
	cursor, _ := servicesCollection.Find(context.Background(), query)

	for cursor.Next(context.TODO()) {
		var service *Service
		err := cursor.Decode(&service)
		if err != nil {
			log.Fatal(err)
		}

		services = append(services, service.PrimaryIdentifier)
	}

	if len(services) == 0 {
		return nil, errors.New("Could not find related Service")
	}

	// Get the relevant Journeys
	framedVehicleJourneyDate, _ := time.Parse(YearMonthDayFormat, identifyingInformation["FramedVehicleJourneyDate"])
	var journeys []*Journey
	vehicleJourneyRef := identifyingInformation["VehicleJourneyRef"]
	journeysCollection := database.GetCollection("journeys")
	query = bson.M{
		"$and": bson.A{
			bson.M{"serviceref": bson.M{"$in": services}},
			bson.M{"otheridentifiers.RealtimeJourneyCode": vehicleJourneyRef},
			bson.M{"$or": bson.A{
				bson.M{"path.originstopref": identifyingInformation["OriginRef"]},
				bson.M{"path.destinationstopref": identifyingInformation["DestinationRef"]},
			}},
		},
	}

	cursor, _ = journeysCollection.Find(context.Background(), query)

	for cursor.Next(context.TODO()) {
		var journey *Journey
		err := cursor.Decode(&journey)
		if err != nil {
			log.Fatal(err)
		}

		if journey.Availability.MatchDate(framedVehicleJourneyDate) {
			journeys = append(journeys, journey)
		}
	}

	if len(journeys) == 0 {
		return nil, errors.New("Could not find related Journeys")
	} else if len(journeys) == 1 {
		return journeys[0], nil
	} else {
		timeFilteredJourneys := []*Journey{}

		for _, journey := range journeys {
			originAimedDepartureTimeNoOffset, _ := time.Parse(XSDDateTimeFormat, identifyingInformation["OriginAimedDepartureTime"])
			originAimedDepartureTime := originAimedDepartureTimeNoOffset.In(currentTime.Location())

			if journey.DepartureTime.Hour() == originAimedDepartureTime.Hour() && journey.DepartureTime.Minute() == originAimedDepartureTime.Minute() {
				timeFilteredJourneys = append(timeFilteredJourneys, journey)
			}
		}

		if len(timeFilteredJourneys) == 0 {
			return nil, errors.New("Could not narrow down to single Journey with departure time. Now zero")
		} else if len(timeFilteredJourneys) == 1 {
			return timeFilteredJourneys[0], nil
		} else {
			return nil, errors.New("Could not narrow down to single Journey by time. Still many remaining")
		}
	}
}

type JourneyPathItem struct {
	OriginStopRef      string `groups:"basic"`
	DestinationStopRef string `groups:"basic"`

	OriginStop      *Stop `groups:"basic"`
	DestinationStop *Stop `groups:"basic"`

	Distance int `groups:"basic"`

	OriginArrivalTime      time.Time `groups:"basic"`
	DestinationArrivalTime time.Time `groups:"basic"`

	OriginDepartureTime time.Time `groups:"basic"`

	DestinationDisplay string `groups:"basic"`

	OriginActivity      []JourneyPathItemActivity `groups:"basic"`
	DestinationActivity []JourneyPathItemActivity `groups:"basic"`

	Track []Location `groups:"basic"`
}

func (jpi *JourneyPathItem) GetReferences() {
	jpi.GetOriginStop()
	jpi.GetDestinationStop()
}
func (jpi *JourneyPathItem) GetOriginStop() {
	stopsCollection := database.GetCollection("stops")
	stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": jpi.OriginStopRef}).Decode(&jpi.OriginStop)
}
func (jpi *JourneyPathItem) GetDestinationStop() {
	stopsCollection := database.GetCollection("stops")
	stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": jpi.DestinationStopRef}).Decode(&jpi.DestinationStop)
}

type JourneyPathItemActivity string

const (
	JourneyPathItemActivityPickup  = "Pickup"
	JourneyPathItemActivitySetdown = "Setdown"
	JourneyPathItemActivityPass    = "Pass"
)
