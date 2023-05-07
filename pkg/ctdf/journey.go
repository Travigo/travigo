package ctdf

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/database"
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

	RealtimeJourney *RealtimeJourney `groups:"basic" bson:"-"`
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
	realtimeActiveCutoffDate := GetActiveRealtimeJourneyCutOffDate()

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	var realtimeJourney *RealtimeJourney
	realtimeJourneysCollection.FindOne(context.Background(), bson.M{
		"journeyref":           j.PrimaryIdentifier,
		"modificationdatetime": bson.M{"$gt": realtimeActiveCutoffDate},
	}).Decode(&realtimeJourney)

	if realtimeJourney != nil && realtimeJourney.IsActive() {
		j.RealtimeJourney = realtimeJourney
	}
}
func (j Journey) MarshalBinary() ([]byte, error) {
	return json.Marshal(j)
}
func (journey *Journey) GenerateFunctionalHash(includeAvailabilityCondition bool) string {
	hash := sha256.New()

	hash.Write([]byte(journey.ServiceRef))
	hash.Write([]byte(journey.DestinationDisplay))
	hash.Write([]byte(journey.Direction))
	hash.Write([]byte(journey.DepartureTime.String()))

	// TODO: REVERT THE CHAGES TO THIS LINE
	// BUT THINK ABOUT IT - WE SHOULD ALWAYS IGNORE AVAILABILITY CONDITIONS WHEN FINDING IDENTICAL JOURNEYS
	// IF WE FILTER OUT BASED ON BEING AVAILABLE TODAY THEN WE SHOULDNT CARE ABOUT THE SPECIFICS OF THE CONDITIONS???
	if includeAvailabilityCondition {
		rules := append(journey.Availability.Match, journey.Availability.MatchSecondary...)
		rules = append(rules, journey.Availability.Exclude...)

		rules = append(rules, journey.Availability.Condition...)

		for _, availabilityMatchRule := range rules {
			hash.Write([]byte(availabilityMatchRule.Type))
			hash.Write([]byte(availabilityMatchRule.Value))
			hash.Write([]byte(availabilityMatchRule.Description))
		}
	}

	for _, pathItem := range journey.Path {
		hash.Write([]byte(pathItem.OriginStopRef))
		hash.Write([]byte(pathItem.OriginArrivalTime.GoString()))
		hash.Write([]byte(pathItem.OriginDepartureTime.GoString()))
		hash.Write([]byte(pathItem.DestinationStopRef))
		hash.Write([]byte(pathItem.DestinationArrivalTime.GoString()))
	}

	return fmt.Sprintf("%x", hash.Sum(nil))
}
func (j Journey) FlattenStops() ([]string, map[string]time.Time, map[string]time.Time) {
	stops := []string{}
	arrivalTimes := map[string]time.Time{}
	departureTimes := map[string]time.Time{}
	alreadySeen := map[string]bool{}

	for _, pathItem := range j.Path {
		if !alreadySeen[pathItem.OriginStopRef] {
			stops = append(stops, pathItem.OriginStopRef)

			arrivalTimes[pathItem.OriginStopRef] = pathItem.OriginArrivalTime
			departureTimes[pathItem.OriginStopRef] = pathItem.OriginDepartureTime

			alreadySeen[pathItem.OriginStopRef] = true
		}
	}

	lastPathItem := j.Path[len(j.Path)-1]
	if !alreadySeen[lastPathItem.OriginStopRef] {
		stops = append(stops, lastPathItem.OriginStopRef)

		arrivalTimes[lastPathItem.OriginStopRef] = lastPathItem.OriginArrivalTime
		departureTimes[lastPathItem.OriginStopRef] = lastPathItem.OriginDepartureTime
	}

	return stops, arrivalTimes, departureTimes
}

func FilterIdenticalJourneys(journeys []*Journey, includeAvailabilityCondition bool) []*Journey {
	filtered := []*Journey{}

	matches := map[string]bool{}
	for _, journey := range journeys {
		hash := journey.GenerateFunctionalHash(includeAvailabilityCondition)

		if !matches[hash] {
			filtered = append(filtered, journey)
			matches[hash] = true
		}
	}

	return filtered
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
