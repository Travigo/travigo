package ctdf

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const XSDDateTimeFormat = "2006-01-02T15:04:05-07:00"

//goland:noinspection GoUnusedConst
const XSDDateTimeWithFractionalFormat = "2006-01-02T15:04:05.999999-07:00"

type Journey struct {
	PrimaryIdentifier string            `groups:"basic,departures-llm,departureboard-cache" bson:",omitempty"`
	OtherIdentifiers  map[string]string `groups:"basic" json:",omitempty" bson:",omitempty"`

	CreationDateTime     time.Time `groups:"detailed" bson:",omitempty"`
	ModificationDateTime time.Time `groups:"detailed" bson:",omitempty"`
	Expiry               time.Time `groups:"detailed" bson:",omitempty"`

	DataSource *DataSourceReference `groups:"detailed" bson:",omitempty"`

	ServiceRef string   `groups:"internal,departureboard-cache" bson:",omitempty"`
	Service    *Service `groups:"basic,departures-llm" json:",omitempty" bson:"-"`

	OperatorRef string    `groups:"internal,departureboard-cache" bson:",omitempty"`
	Operator    *Operator `groups:"basic,departures-llm" json:",omitempty" bson:"-"`

	Direction         string    `groups:"detailed" json:",omitempty" bson:",omitempty"`
	DepartureTime     time.Time `groups:"basic,departures-llm,departureboard-cache" bson:",omitempty"`
	DepartureTimezone string    `groups:"basic,departureboard-cache" bson:",omitempty"`

	Track []Location `groups:"detailed" bson:",omitempty"`

	DestinationDisplay string `groups:"basic,departures-llm,departureboard-cache" bson:",omitempty"`

	Availability *Availability `groups:"internal,departureboard-cache" bson:",omitempty"`

	Path []*JourneyPathItem `groups:"detailed,departureboard-cache" bson:",omitempty"`

	RealtimeJourney *RealtimeJourney `groups:"basic" bson:"-"`

	// Detailed journey information
	DetailedRailInformation *JourneyDetailedRail `groups:"detailed" bson:",omitempty"`
}

func (j *Journey) GetReferences() {
	j.GetOperator()
	j.GetService()
}
func (j *Journey) GetOperator() {
	if j.Operator != nil {
		return
	}

	operatorsCollection := database.GetCollection("operators")
	query := bson.M{"$or": bson.A{bson.M{"primaryidentifier": j.OperatorRef}, bson.M{"otheridentifiers": j.OperatorRef}}}
	operatorsCollection.FindOne(context.Background(), query).Decode(&j.Operator)
}
func (j *Journey) GetService() {
	if j.Service != nil {
		return
	}

	servicesCollection := database.GetCollection("services")
	servicesCollection.FindOne(context.Background(), bson.M{"$or": bson.A{
		bson.M{"primaryidentifier": j.ServiceRef},
		bson.M{"otheridentifiers": j.ServiceRef},
	}}).Decode(&j.Service)
}
func (j *Journey) GetDeepReferences() {
	wg := sync.WaitGroup{}
	for _, path := range j.Path {
		wg.Add(1)
		go func(path *JourneyPathItem) {
			path.GetReferences()

			wg.Done()
		}(path)
	}

	wg.Wait()
}
func (j *Journey) GetRealtimeJourney(opts *options.FindOneOptions) {
	realtimeActiveCutoffDate := GetActiveRealtimeJourneyCutOffDate()

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	var realtimeJourney *RealtimeJourney
	realtimeJourneysCollection.FindOne(context.Background(), bson.M{
		"journey.primaryidentifier": j.PrimaryIdentifier,
		"modificationdatetime":      bson.M{"$gt": realtimeActiveCutoffDate},
	}, opts).Decode(&realtimeJourney)

	if realtimeJourney != nil && realtimeJourney.IsActive() {
		j.RealtimeJourney = realtimeJourney
	}
}
func (j Journey) MarshalBinary() ([]byte, error) {
	return json.Marshal(j)
}
func (j *Journey) GenerateFunctionalHash(includeAvailabilityCondition bool) string {
	hash := sha256.New()

	hash.Write([]byte(j.ServiceRef))
	hash.Write([]byte(j.DestinationDisplay))
	hash.Write([]byte(j.Direction))
	hash.Write([]byte(j.DepartureTime.String()))

	// TODO: REVERT THE CHAGES TO THIS LINE
	// BUT THINK ABOUT IT - WE SHOULD ALWAYS IGNORE AVAILABILITY CONDITIONS WHEN FINDING IDENTICAL JOURNEYS
	// IF WE FILTER OUT BASED ON BEING AVAILABLE TODAY THEN WE SHOULDNT CARE ABOUT THE SPECIFICS OF THE CONDITIONS???
	if includeAvailabilityCondition {
		rules := append(j.Availability.Match, j.Availability.MatchSecondary...)
		rules = append(rules, j.Availability.Exclude...)

		rules = append(rules, j.Availability.Condition...)

		for _, availabilityMatchRule := range rules {
			hash.Write([]byte(availabilityMatchRule.Type))
			hash.Write([]byte(availabilityMatchRule.Value))
			hash.Write([]byte(availabilityMatchRule.Description))
		}
	}

	for _, pathItem := range j.Path {
		hash.Write([]byte(pathItem.OriginStopRef))
		hash.Write([]byte(pathItem.OriginArrivalTime.GoString()))
		hash.Write([]byte(pathItem.OriginDepartureTime.GoString()))
		hash.Write([]byte(pathItem.DestinationStopRef))
		hash.Write([]byte(pathItem.DestinationArrivalTime.GoString()))
	}

	return fmt.Sprintf("%x", hash.Sum(nil))
}
func (j Journey) FlattenStops() ([]string, map[string]time.Time, map[string]time.Time) {
	var stops []string
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
	var filtered []*Journey

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
	OriginStopRef      string `groups:"basic,departureboard-cache"`
	DestinationStopRef string `groups:"basic,departureboard-cache"`

	OriginStop      *Stop `groups:"basic"`
	DestinationStop *Stop `groups:"basic"`

	OriginPlatform      string `groups:"basic"`
	DestinationPlatform string `groups:"basic"`

	Distance int `groups:"basic"`

	OriginArrivalTime      time.Time `groups:"basic,departureboard-cache"`
	DestinationArrivalTime time.Time `groups:"basic,departureboard-cache"`

	OriginDepartureTime time.Time `groups:"basic,departureboard-cache"`

	DestinationDisplay string `groups:"basic,departureboard-cache"`

	OriginActivity      []JourneyPathItemActivity `groups:"basic,departureboard-cache"`
	DestinationActivity []JourneyPathItemActivity `groups:"basic"`

	Track []Location `groups:"basic"`

	// Associations []*Association `groups:"detailed" bson:",omitempty"`
}

func (jpi *JourneyPathItem) GetReferences() {
	jpi.GetOriginStop()
	jpi.GetDestinationStop()
}
func (jpi *JourneyPathItem) GetOriginStop() {
	stopsCollection := database.GetCollection("stops")
	stopsCollection.FindOne(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": jpi.OriginStopRef},
			bson.M{"otheridentifiers": jpi.OriginStopRef},
		},
	}).Decode(&jpi.OriginStop)
}
func (jpi *JourneyPathItem) GetDestinationStop() {
	stopsCollection := database.GetCollection("stops")
	stopsCollection.FindOne(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": jpi.DestinationStopRef},
			bson.M{"otheridentifiers": jpi.DestinationStopRef},
		},
	}).Decode(&jpi.DestinationStop)
}

type JourneyPathItemActivity string

const (
	JourneyPathItemActivityPickup  = "Pickup"
	JourneyPathItemActivitySetdown = "Setdown"
	JourneyPathItemActivityPass    = "Pass"
)
