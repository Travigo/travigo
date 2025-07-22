package ctdf

import (
	"time"
)

var RealtimeJourneyIDFormat = "realtime-%s:%s"

type RealtimeJourney struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"detailed"`

	ActivelyTracked bool `groups:"basic"`

	Journey *Journey `groups:"basic"`

	Service *Service `groups:"internal"`

	JourneyRunDate time.Time `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"basic"`

	TimeoutDurationMinutes int `groups:"internal"`

	DataSource *DataSourceReference `groups:"internal"`

	VehicleLocation            Location `groups:"basic" bson:",omitempty"`
	VehicleLocationDescription string   `groups:"basic"`
	VehicleBearing             float64  `groups:"basic"`

	DepartedStopRef string `groups:"basic"`
	DepartedStop    *Stop  `groups:"basic" bson:"-"`

	NextStopRef string `groups:"basic"`
	NextStop    *Stop  `groups:"basic" bson:"-"`

	Stops  map[string]*RealtimeJourneyStops `groups:"basic"` // Historic & future estimates
	Offset time.Duration                    `groups:"internal"`

	Reliability RealtimeJourneyReliabilityType `groups:"basic"`

	VehicleRef string `groups:"internal"`

	Cancelled bool `groups:"basic"`

	Occupancy RealtimeJourneyOccupancy `groups:"detailed"`

	// Detailed realtime journey information
	DetailedRailInformation RealtimeJourneyDetailedRail `groups:"detailed"`
}

type RealtimeJourneyOccupancy struct {
	OccupancyAvailable bool `groups:"basic"`

	ActualValues          bool `groups:"basic"`
	WheelchairInformation bool `groups:"basic"`
	SeatedInformation     bool `groups:"basic"`

	TotalPercentageOccupancy int `groups:"basic"`

	Capacity           int `groups:"basic"`
	SeatedCapacity     int `groups:"basic"`
	WheelchairCapacity int `groups:"basic"`

	Occupancy           int `groups:"basic"`
	SeatedOccupancy     int `groups:"basic"`
	WheelchairOccupancy int `groups:"basic"`
}

type RealtimeJourneyReliabilityType string

const (
	RealtimeJourneyReliabilityExternalProvided     RealtimeJourneyReliabilityType = "ExternalProvided"
	RealtimeJourneyReliabilityLocationWithTrack    RealtimeJourneyReliabilityType = "LocationWithTrack"
	RealtimeJourneyReliabilityLocationWithoutTrack RealtimeJourneyReliabilityType = "LocationWithoutTrack"
)

func (r *RealtimeJourney) IsActive() bool {
	timedOut := (time.Since(r.ModificationDateTime)).Minutes() > float64(r.TimeoutDurationMinutes)

	if timedOut {
		return false
	}

	// If tis still nil then give up
	if r.Journey == nil {
		return false
	}

	// If the path is nil then we cant do the following checks so just assume true
	if len(r.Journey.Path) == 0 {
		return true
	}

	lastPathItem := r.Journey.Path[len(r.Journey.Path)-1]

	if lastPathItem.DestinationStop == nil {
		lastPathItem.GetDestinationStop()

		// If we still cant find it then mark as in-active
		if lastPathItem.DestinationStop == nil {
			return false
		}
	}

	// No proper location then give up and say valid
	if r.VehicleLocation.Type != "Point" {
		return true
	}

	now := time.Now()
	lastPathItemArrivalDateless := lastPathItem.DestinationArrivalTime
	lastPathItemArrival := time.Date(
		now.Year(), now.Month(), now.Day(), lastPathItemArrivalDateless.Hour(), lastPathItemArrivalDateless.Minute(), lastPathItemArrivalDateless.Second(), lastPathItemArrivalDateless.Nanosecond(), now.Location(),
	)
	timeFromlastPathItemArrival := lastPathItemArrival.Sub(now).Minutes()

	distanceEndStopLocation := r.VehicleLocation.Distance(lastPathItem.DestinationStop.Location)

	// If we're past the last path item arrival time & vehicle location is less than 150m from it then class journey as in-active
	return !((timeFromlastPathItemArrival < 0) && (distanceEndStopLocation < 150))
}

type RealtimeJourneyStops struct {
	StopRef string `groups:"basic"`
	Stop    *Stop  `groups:"basic" bson:"-"`

	Platform string `groups:"basic"`

	ArrivalTime   time.Time `groups:"basic"`
	DepartureTime time.Time `groups:"basic"`

	TimeType RealtimeJourneyStopTimeType `groups:"basic"`

	Cancelled bool `groups:"basic"`
}

type RealtimeJourneyStopTimeType string

const (
	// Unknown         RealtimeJourneyStopTimeType = "Unknown"
	RealtimeJourneyStopTimeHistorical      RealtimeJourneyStopTimeType = "Historical"
	RealtimeJourneyStopTimeEstimatedFuture RealtimeJourneyStopTimeType = "EstimatedFuture"
)

func GetShortActiveRealtimeJourneyCutOffDate() time.Time {
	return time.Now().Add(-60 * time.Minute)
}

func GetActiveRealtimeJourneyCutOffDate() time.Time {
	return time.Now().Add(-240 * time.Minute)
}
