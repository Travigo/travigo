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

//goland:noinspection GoUnusedConst
const XSDDateTimeWithFractionalFormat = "2006-01-02T15:04:05.999999-07:00"

type Journey struct {
	PrimaryIdentifier string            `groups:"basic,departures-llm,departureboard-cache,web-board,web-journey,web-planner,web-saved,web-notification" bson:",omitempty"`
	OtherIdentifiers  map[string]string `groups:"basic,departureboard-cache" json:",omitempty" bson:",omitempty"`

	CreationDateTime     time.Time `groups:"detailed" bson:",omitempty"`
	ModificationDateTime time.Time `groups:"detailed" bson:",omitempty"`
	Expiry               time.Time `groups:"detailed" bson:",omitempty"`

	DataSource *DataSourceReference `groups:"detailed,departureboard-cache,web-journey" bson:",omitempty"`

	ServiceRef string   `groups:"internal,departureboard-cache" bson:",omitempty"`
	Service    *Service `groups:"basic,departures-llm,web-board,web-journey,web-planner,web-saved,web-notification" json:",omitempty" bson:"-"`

	OperatorRef string    `groups:"internal,departureboard-cache" bson:",omitempty"`
	Operator    *Operator `groups:"basic,departures-llm,web-planner,web-notification" json:",omitempty" bson:"-"`

	Direction            string    `groups:"detailed" json:",omitempty" bson:",omitempty"`
	ShortName            string    `groups:"detailed" json:",omitempty" bson:",omitempty"`
	WheelchairAccessible int8      `groups:"detailed" bson:",omitempty"`
	BikesAllowed         int8      `groups:"detailed" bson:",omitempty"`
	DepartureTime        time.Time `groups:"basic,departures-llm,departureboard-cache,web-journey,web-planner,web-saved" bson:",omitempty"`
	DepartureTimezone    string    `groups:"basic,departureboard-cache,web-journey,web-planner,web-saved" bson:",omitempty"`

	Track               []Location           `groups:"detailed,web-journey" bson:"-"`
	TrackRef            string               `groups:"internal" bson:",omitempty"`
	TrackDataSource     *DataSourceReference `groups:"detailed,web-journey" bson:"-"`
	DestinationDisplay  string               `groups:"basic,departures-llm,departureboard-cache,web-journey,web-saved,web-notification" bson:",omitempty"`
	OriginDisplay       string               `groups:"web-saved,web-notification" bson:"-" json:",omitempty"`
	ReplacesJourneyRefs []string             `groups:"basic,departureboard-cache" bson:",omitempty"`

	// AmendmentStopRefs includes original calls affected by the amendment.
	AmendmentStopRefs []string `json:"-" bson:",omitempty"`

	Availability *Availability `groups:"internal,departureboard-cache" bson:",omitempty"`

	Calls               []JourneyCall `groups:"detailed,departureboard-cache,web-journey" bson:"calls"`
	OriginStopRefs      []string      `bson:"originstoprefs,omitempty" json:"-"`
	DestinationStopRefs []string      `bson:"destinationstoprefs,omitempty" json:"-"`

	RealtimeJourney *RealtimeJourney `groups:"basic,web-board,web-journey,web-planner" bson:"-"`

	// Detailed journey information
	DetailedRailInformation *JourneyDetailedRail `groups:"detailed,web-journey,departureboard-cache" bson:",omitempty"`
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
	if includeAvailabilityCondition && j.Availability != nil {
		for _, rules := range [][]AvailabilityRule{
			j.Availability.Match, j.Availability.MatchSecondary, j.Availability.Exclude, j.Availability.Condition,
		} {
			for _, rule := range rules {
				hash.Write([]byte(rule.Type))
				hash.Write([]byte(rule.Value))
				hash.Write([]byte(rule.Description))
			}
		}
	}

	for origin, destination := range j.LegCalls() {
		hash.Write([]byte(origin.StopRef))
		hash.Write([]byte(origin.Arrival.GoString()))
		hash.Write([]byte(origin.Departure.GoString()))
		hash.Write([]byte(destination.StopRef))
		hash.Write([]byte(destination.Arrival.GoString()))
	}

	return fmt.Sprintf("%x", hash.Sum(nil))
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

// JourneyPathItem represents a timing link used when building calls from feed
// data. Journeys persist the calls directly.
type JourneyPathItem struct {
	// PassingPath holds ordered CIF TIPLOC references between passenger calls.
	PassingPath []string `bson:"passingpath,omitempty" json:"-"`

	OriginStopRef      string `groups:"basic,departureboard-cache,web-journey"`
	DestinationStopRef string `groups:"basic,departureboard-cache,web-journey"`

	OriginStop      *Stop `groups:"basic,web-journey"`
	DestinationStop *Stop `groups:"basic,web-journey"`

	OriginPlatform      string `groups:"basic,departureboard-cache,web-journey"`
	DestinationPlatform string `groups:"basic,departureboard-cache,web-journey"`

	Distance int `groups:"basic"`

	OriginArrivalTime      time.Time `groups:"basic,departureboard-cache,web-journey"`
	DestinationArrivalTime time.Time `groups:"basic,departureboard-cache,web-journey"`

	OriginDepartureTime time.Time `groups:"basic,departureboard-cache,web-journey"`

	DestinationDisplay string `groups:"basic,departureboard-cache"`

	OriginActivity      []JourneyPathItemActivity `groups:"basic,departureboard-cache,web-journey"`
	DestinationActivity []JourneyPathItemActivity `groups:"basic,departureboard-cache,web-journey"`

	Track           []Location           `groups:"basic,web-journey"`
	TrackRef        string               `groups:"internal" bson:",omitempty"`
	TrackDataSource *DataSourceReference `groups:"basic,web-journey" bson:"-"`

	// Associations []*Association `groups:"detailed" bson:",omitempty"`
}

type JourneyPathItemActivity string

const (
	JourneyPathItemActivityPickup  = "Pickup"
	JourneyPathItemActivitySetdown = "Setdown"
	JourneyPathItemActivityPass    = "Pass"
)

func (c *JourneyCall) GetStop() {
	if c.Stop != nil || c.StopRef == "" {
		return
	}
	database.GetCollection("stops").FindOne(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": c.StopRef},
			bson.M{"otheridentifiers": c.StopRef},
		},
	}).Decode(&c.Stop)
}

// GetStopRefs returns the distinct stop references in visit order.
func (j *Journey) GetStopRefs() []string {
	refs := make([]string, 0, len(j.Calls))
	seen := make(map[string]struct{}, len(j.Calls))
	for _, call := range j.Calls {
		if _, found := seen[call.StopRef]; !found && call.StopRef != "" {
			refs = append(refs, call.StopRef)
			seen[call.StopRef] = struct{}{}
		}
	}
	return refs
}

func (j *Journey) ApplyRouteAmendment(amendment *Journey) {
	j.Calls = amendment.Calls
	j.OriginStopRefs = amendment.OriginStopRefs
	j.DestinationStopRefs = amendment.DestinationStopRefs
	j.DepartureTime = amendment.DepartureTime
	j.DestinationDisplay = amendment.DestinationDisplay
	j.Track, j.TrackRef, j.TrackDataSource = nil, "", nil
}
