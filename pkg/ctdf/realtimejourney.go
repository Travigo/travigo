package ctdf

import (
	"fmt"
	"time"
)

var RealtimeJourneyIDFormat = "realtime-%s:%s"

type RealtimeJourney struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"detailed"`

	ActivelyTracked bool `groups:"basic,web-journey,web-journey-realtime,web-planner"`

	Journey *Journey `groups:"basic"`

	Service *Service `groups:"internal"`

	JourneyRunDate time.Time `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"basic"`

	TimeoutDurationMinutes int `groups:"internal"`

	DataSource *DataSourceReference `groups:"internal"`

	VehicleLocation            Location   `groups:"basic,web-journey,web-journey-realtime" bson:",omitempty"`
	VehicleLocationDescription string     `groups:"basic,web-journey,web-journey-realtime"`
	VehicleLocationObservedAt  *time.Time `json:",omitempty" groups:"basic,web-journey,web-journey-realtime" bson:"-"`
	VehicleLocationSource      string     `json:",omitempty" groups:"basic,web-journey,web-journey-realtime" bson:"-"`
	VehicleLocationConfidence  string     `json:",omitempty" groups:"basic,web-journey,web-journey-realtime" bson:"-"`
	VehicleBearing             float64    `groups:"basic,web-journey,web-journey-realtime"`

	DepartedStopRef string `groups:"basic"`
	DepartedStop    *Stop  `groups:"basic" bson:"-"`

	NextStopRef string `groups:"basic,web-journey,web-journey-realtime"`
	// NextStopIndex identifies the next call in the journey's ordered stop list.
	// NextStopRef alone is ambiguous when a journey revisits a stop.
	NextStopIndex int   `groups:"basic,web-journey,web-journey-realtime"`
	NextStop      *Stop `groups:"basic" bson:"-"`

	Stops  map[string]*RealtimeJourneyStops `groups:"basic,web-journey,web-journey-realtime"` // Historic & future estimates
	Offset time.Duration                    `groups:"internal"`

	Reliability RealtimeJourneyReliabilityType `groups:"basic,web-board,web-journey,web-journey-realtime"`

	VehicleRef string `groups:"internal"`

	Cancelled bool `groups:"basic,web-journey,web-journey-realtime"`

	// SuppressFromDepartures hides this journey from both stop-board modes for
	// the listed service dates when it has been replaced by an STP overlay.
	SuppressFromDepartures     bool     `groups:"internal"`
	SuppressFromDepartureDates []string `groups:"internal"`
	ReplacedByJourneyRef       string   `groups:"basic"`

	Occupancy RealtimeJourneyOccupancy `groups:"detailed,web-journey,web-journey-realtime"`

	// Detailed realtime journey information
	DetailedRailInformation JourneyDetailedRail `groups:"detailed,web-journey,web-journey-realtime"`
}

func (r *RealtimeJourney) SuppressesBoardAt(date time.Time) bool {
	if r == nil || !r.SuppressFromDepartures {
		return false
	}
	if len(r.SuppressFromDepartureDates) == 0 {
		return true
	}

	serviceDate := date.Format(YearMonthDayFormat)
	for _, suppressedDate := range r.SuppressFromDepartureDates {
		if suppressedDate == serviceDate {
			return true
		}
	}
	return false
}

type RealtimeJourneyOccupancy struct {
	OccupancyAvailable bool `groups:"basic,web-journey,web-journey-realtime"`

	ActualValues          bool `groups:"basic"`
	WheelchairInformation bool `groups:"basic,web-journey,web-journey-realtime"`
	SeatedInformation     bool `groups:"basic,web-journey,web-journey-realtime"`

	TotalPercentageOccupancy int `groups:"basic,web-journey,web-journey-realtime"`

	Capacity           int `groups:"basic"`
	SeatedCapacity     int `groups:"basic,web-journey,web-journey-realtime"`
	WheelchairCapacity int `groups:"basic,web-journey,web-journey-realtime"`

	Occupancy           int `groups:"basic"`
	SeatedOccupancy     int `groups:"basic,web-journey,web-journey-realtime"`
	WheelchairOccupancy int `groups:"basic,web-journey,web-journey-realtime"`
}

type RealtimeJourneyReliabilityType string

const (
	RealtimeJourneyReliabilityExternalProvided     RealtimeJourneyReliabilityType = "ExternalProvided"
	RealtimeJourneyReliabilityLocationWithTrack    RealtimeJourneyReliabilityType = "LocationWithTrack"
	RealtimeJourneyReliabilityLocationWithoutTrack RealtimeJourneyReliabilityType = "LocationWithoutTrack"
)

func (r *RealtimeJourney) IsActive() bool {
	timedOut := (time.Since(r.ModificationDateTime)).Minutes() > float64(r.TimeoutDurationMinutes)

	if timedOut && r.TimeoutDurationMinutes > 0 {
		return false
	}

	// If tis still nil then give up
	if r.Journey == nil {
		return false
	}

	// If the path is nil then we cant do the following checks so just assume true
	if len(r.Journey.Calls) == 0 {
		return true
	}

	terminal := &r.Journey.Calls[len(r.Journey.Calls)-1]

	if terminal.Stop == nil {
		terminal.GetStop()

		// If we still cant find it then mark as in-active
		if terminal.Stop == nil {
			return false
		}
	}

	// No proper location then give up and say valid
	if r.VehicleLocation.Type != "Point" || len(r.VehicleLocation.Coordinates) < 2 ||
		terminal.Stop.Location == nil || len(terminal.Stop.Location.Coordinates) < 2 {
		return true
	}

	now := time.Now()
	lastPathItemArrival := terminal.Arrival
	if lastPathItemArrival.IsZero() {
		lastPathItemArrival = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	} else if r.Journey != nil {
		serviceDate := now
		if !r.JourneyRunDate.IsZero() {
			// JourneyRunDate is a service-date label, so keep its calendar fields
			// while using the process location for schedule-time conversion.
			runDate := r.JourneyRunDate
			serviceDate = time.Date(runDate.Year(), runDate.Month(), runDate.Day(), 0, 0, 0, 0, now.Location())
		}
		lastPathItemArrival = boardScheduledTimeOnServiceDate(r.Journey, r.Journey.LegCount()-1, serviceDate, BoardTypeArrival)
	}
	timeFromlastPathItemArrival := lastPathItemArrival.Sub(now).Minutes()

	distanceEndStopLocation := r.VehicleLocation.Distance(terminal.Stop.Location)

	// If we're past the last path item arrival time & vehicle location is less than 150m from it then class journey as in-active
	return !((timeFromlastPathItemArrival < 0) && (distanceEndStopLocation < 150))
}

type RealtimeJourneyStops struct {
	StopRef string `groups:"basic,web-journey,web-journey-realtime"`
	// JourneyStopIndex identifies this call in the ordered journey stop list.
	// Stop references are not unique on circular journeys.
	JourneyStopIndex int   `groups:"basic,web-journey,web-journey-realtime"`
	Stop             *Stop `groups:"basic" bson:"-"`

	Platform string `groups:"basic,web-journey,web-journey-realtime"`

	ArrivalTime   time.Time `groups:"basic,web-journey,web-journey-realtime"`
	DepartureTime time.Time `groups:"basic,web-journey,web-journey-realtime"`

	TimeType RealtimeJourneyStopTimeType `groups:"basic,web-journey,web-journey-realtime"`

	Cancelled bool `groups:"basic,web-journey,web-journey-realtime"`
}

func RealtimeJourneyStopKey(stopRef string, journeyStopIndex int) string {
	return fmt.Sprintf("%s@%d", stopRef, journeyStopIndex)
}

// RealtimeStop returns a stop call by occurrence. It also reads the historic
// stop-ref-keyed representation so journeys already in storage remain usable.
func (r *RealtimeJourney) RealtimeStop(stopRef string, journeyStopIndex int) *RealtimeJourneyStops {
	if r == nil {
		return nil
	}
	if stop := r.Stops[RealtimeJourneyStopKey(stopRef, journeyStopIndex)]; stop != nil {
		return stop
	}
	for _, stop := range r.Stops {
		if stop != nil && stop.StopRef == stopRef && stop.JourneyStopIndex == journeyStopIndex {
			return stop
		}
	}
	// A legacy record contains no occurrence information. It is safe at a
	// unique stop, or at the first call of a repeated stop.
	if legacyStop := r.Stops[stopRef]; legacyStop != nil && r.legacyStopCanMatch(stopRef, journeyStopIndex) {
		return legacyStop
	}
	return nil
}

func (r *RealtimeJourney) legacyStopCanMatch(stopRef string, journeyStopIndex int) bool {
	if r.Journey == nil || len(r.Journey.Calls) == 0 {
		return true
	}
	matchCount := 0
	firstMatchIndex := -1
	if r.Journey.Calls[0].StopRef == stopRef {
		firstMatchIndex = 0
		matchCount++
	}
	index := 0
	for _, destination := range r.Journey.LegCalls() {
		if destination.StopRef == stopRef {
			if firstMatchIndex < 0 {
				firstMatchIndex = index + 1
			}
			matchCount++
		}
		index++
	}
	return matchCount <= 1 || journeyStopIndex == firstMatchIndex
}

func (r *RealtimeJourney) SetRealtimeStop(stop *RealtimeJourneyStops) {
	if r == nil || stop == nil || stop.StopRef == "" {
		return
	}
	if r.Stops == nil {
		r.Stops = map[string]*RealtimeJourneyStops{}
	}
	r.Stops[RealtimeJourneyStopKey(stop.StopRef, stop.JourneyStopIndex)] = stop
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
