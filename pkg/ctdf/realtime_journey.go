package ctdf

import (
	"context"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

var RealtimeJourneyIDFormat = "REALTIME:%s:%s"

type RealtimeJourney struct {
	PrimaryIdentifier string `groups:"basic"`

	JourneyRef string   `groups:"internal"`
	Journey    *Journey `groups:"basic" bson:"-"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	VehicleLocation Location `groups:"basic"`
	VehicleBearing  float64  `groups:"basic"`

	DepartedStopRef string `groups:"basic"`
	DepartedStop    *Stop  `groups:"basic" bson:"-"`

	NextStopRef string `groups:"basic"`
	NextStop    *Stop  `groups:"basic" bson:"-"`

	NextStopArrival   time.Time `groups:"basic"`
	NextStopDeparture time.Time `groups:"basic"`

	Stops map[string]*RealtimeJourneyStops `groups:"basic"` // Historic & future estimates
}

func (r *RealtimeJourney) GetReferences() {
	r.GetJourney()
}
func (r *RealtimeJourney) GetJourney() {
	journeysCollection := database.GetCollection("journeys")
	journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": r.JourneyRef}).Decode(&r.Journey)
}

func (r *RealtimeJourney) IsActive() bool {
	timedOut := (time.Now().Sub(r.ModificationDateTime)).Minutes() > 10

	// lastPathItem := r.Journey.Path[len(r.Journey.Path)-1]
	// // now := time.Now()
	// // nearEndTime := lastPathItem.DestinationArrivalTime.Sub(now)

	// pretty.Println(lastPathItem.DestinationStop.Location)
	// // nearEndStopLocation := r.VehicleLocation.Distance(lastPathItem.DestinationStop.Location)

	return !timedOut
}

type RealtimeJourneyStops struct {
	StopRef string `groups:"basic"`
	Stop    *Stop  `groups:"basic" bson:"-"`

	ArrivalTime   time.Time `groups:"basic"`
	DepartureTime time.Time `groups:"basic"`

	TimeType RealtimeJourneyStopTimeType `groups:"basic"`
}

type RealtimeJourneyStopTimeType string

const (
	// Unknown         RealtimeJourneyStopTimeType = "Unknown"
	RealtimeJourneyStopTimeHistorical      RealtimeJourneyStopTimeType = "Historical"
	RealtimeJourneyStopTimeEstimatedFuture                             = "EstimatedFuture"
)

func GetActiveRealtimeJourneyCutOffDate() time.Time {
	return time.Now().Add(-10 * time.Minute)
}
