package ctdf

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/cespare/xxhash/v2"
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
	// Two independently seeded hashes keep a 128-bit fingerprint while avoiding
	// timestamp formatting and its per-call allocations.
	var first, second xxhash.Digest
	first.Reset()
	second.ResetWithSeed(1)
	write := func(data []byte) {
		first.Write(data)
		second.Write(data)
	}
	var buffer [16]byte
	writeString := func(value string) {
		binary.LittleEndian.PutUint64(buffer[:8], uint64(len(value)))
		write(buffer[:8])
		first.WriteString(value)
		second.WriteString(value)
	}
	writeTime := func(value time.Time) {
		year, month, day := value.Date()
		hour, minute, second := value.Clock()
		binary.LittleEndian.PutUint64(buffer[:8], uint64(int64(year)))
		buffer[8], buffer[9], buffer[10] = byte(month), byte(day), byte(hour)
		buffer[11], buffer[12] = byte(minute), byte(second)
		write(buffer[:13])
		binary.LittleEndian.PutUint32(buffer[:4], uint32(value.Nanosecond()))
		write(buffer[:4])
		// GoString distinguishes the UTC and Local singleton locations from
		// other locations, even if they have the same name.
		buffer[0] = 0
		if value.Location() == time.UTC {
			buffer[0] = 1
		} else if value.Location() == time.Local {
			buffer[0] = 2
		}
		write(buffer[:1])
		writeString(value.Location().String())
	}

	writeString(j.ServiceRef)
	writeString(j.DestinationDisplay)
	writeString(j.Direction)
	writeString(j.DepartureTime.String())

	// TODO: REVERT THE CHAGES TO THIS LINE
	// BUT THINK ABOUT IT - WE SHOULD ALWAYS IGNORE AVAILABILITY CONDITIONS WHEN FINDING IDENTICAL JOURNEYS
	// IF WE FILTER OUT BASED ON BEING AVAILABLE TODAY THEN WE SHOULDNT CARE ABOUT THE SPECIFICS OF THE CONDITIONS???
	if includeAvailabilityCondition && j.Availability != nil {
		for _, rules := range [][]AvailabilityRule{
			j.Availability.Match, j.Availability.MatchSecondary, j.Availability.Exclude, j.Availability.Condition,
		} {
			for _, rule := range rules {
				writeString(string(rule.Type))
				writeString(rule.Value)
				writeString(rule.Description)
			}
		}
	}

	for origin, destination := range j.LegCalls() {
		writeString(origin.StopRef)
		writeTime(origin.Arrival)
		writeTime(origin.Departure)
		writeString(destination.StopRef)
		writeTime(destination.Arrival)
	}

	binary.BigEndian.PutUint64(buffer[:8], first.Sum64())
	binary.BigEndian.PutUint64(buffer[8:], second.Sum64())
	return hex.EncodeToString(buffer[:])
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
