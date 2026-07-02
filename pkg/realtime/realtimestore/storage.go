package realtimestore

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type storedRealtimeJourney struct {
	PrimaryIdentifier      string                              `json:"id"`
	OtherIdentifiers       map[string]string                   `json:"oi,omitempty"`
	ActivelyTracked        bool                                `json:"at"`
	Journey                *storedJourney                      `json:"j,omitempty"`
	Service                *storedService                      `json:"s,omitempty"`
	JourneyRunDate         time.Time                           `json:"jrd,omitempty"`
	CreationDateTime       time.Time                           `json:"cdt,omitempty"`
	ModificationDateTime   time.Time                           `json:"mdt"`
	TimeoutDurationMinutes int                                 `json:"to,omitempty"`
	DataSource             *ctdf.DataSourceReference           `json:"ds,omitempty"`
	VehicleLocation        *ctdf.Location                      `json:"vl,omitempty"`
	VehicleLocationDesc    string                              `json:"vld,omitempty"`
	VehicleBearing         float64                             `json:"vb,omitempty"`
	DepartedStopRef        string                              `json:"dep,omitempty"`
	NextStopRef            string                              `json:"next,omitempty"`
	Stops                  map[string]*storedRealtimeStop      `json:"st,omitempty"`
	Offset                 time.Duration                       `json:"off,omitempty"`
	Reliability            ctdf.RealtimeJourneyReliabilityType `json:"rel,omitempty"`
	VehicleRef             string                              `json:"veh,omitempty"`
	Cancelled              bool                                `json:"can,omitempty"`
	Occupancy              *ctdf.RealtimeJourneyOccupancy      `json:"occ,omitempty"`
	DetailedRail           *ctdf.JourneyDetailedRail           `json:"rail,omitempty"`
}

type storedJourney struct {
	PrimaryIdentifier  string          `json:"id"`
	ServiceRef         string          `json:"sr,omitempty"`
	Service            *storedService  `json:"s,omitempty"`
	OperatorRef        string          `json:"or,omitempty"`
	Operator           *storedOperator `json:"op,omitempty"`
	DepartureTimezone  string          `json:"tz,omitempty"`
	DestinationDisplay string          `json:"dest,omitempty"`
}

type storedService struct {
	PrimaryIdentifier    string             `json:"id,omitempty"`
	ServiceName          string             `json:"name,omitempty"`
	OperatorRef          string             `json:"or,omitempty"`
	BrandColour          string             `json:"bc,omitempty"`
	SecondaryBrandColour string             `json:"sbc,omitempty"`
	BrandIcon            string             `json:"bi,omitempty"`
	BrandDisplayMode     string             `json:"bdm,omitempty"`
	TransportType        ctdf.TransportType `json:"tt,omitempty"`
}

type storedOperator struct {
	PrimaryIdentifier string             `json:"id,omitempty"`
	PrimaryName       string             `json:"name,omitempty"`
	TransportType     ctdf.TransportType `json:"tt,omitempty"`
}

type storedRealtimeStop struct {
	StopRef       string                           `json:"sr,omitempty"`
	Platform      string                           `json:"p,omitempty"`
	ArrivalTime   time.Time                        `json:"a,omitempty"`
	DepartureTime time.Time                        `json:"d,omitempty"`
	TimeType      ctdf.RealtimeJourneyStopTimeType `json:"t,omitempty"`
	Cancelled     bool                             `json:"c,omitempty"`
}

func storedRealtimeJourneyFromCTDF(realtimeJourney *ctdf.RealtimeJourney) *storedRealtimeJourney {
	if realtimeJourney == nil {
		return nil
	}

	stored := &storedRealtimeJourney{
		PrimaryIdentifier:      realtimeJourney.PrimaryIdentifier,
		OtherIdentifiers:       realtimeJourney.OtherIdentifiers,
		ActivelyTracked:        realtimeJourney.ActivelyTracked,
		Journey:                storedJourneyFromCTDF(realtimeJourney.Journey, realtimeJourney.PrimaryIdentifier),
		Service:                storedServiceFromCTDF(realtimeJourney.Service),
		JourneyRunDate:         realtimeJourney.JourneyRunDate,
		CreationDateTime:       realtimeJourney.CreationDateTime,
		ModificationDateTime:   realtimeJourney.ModificationDateTime,
		TimeoutDurationMinutes: realtimeJourney.TimeoutDurationMinutes,
		DataSource:             realtimeJourney.DataSource,
		VehicleLocationDesc:    realtimeJourney.VehicleLocationDescription,
		VehicleBearing:         realtimeJourney.VehicleBearing,
		DepartedStopRef:        realtimeJourney.DepartedStopRef,
		NextStopRef:            realtimeJourney.NextStopRef,
		Stops:                  storedRealtimeStopsFromCTDF(realtimeJourney.Stops),
		Offset:                 realtimeJourney.Offset,
		Reliability:            realtimeJourney.Reliability,
		VehicleRef:             realtimeJourney.VehicleRef,
		Cancelled:              realtimeJourney.Cancelled,
	}

	if realtimeJourney.VehicleLocation.Type != "" {
		location := realtimeJourney.VehicleLocation
		stored.VehicleLocation = &location
	}
	if hasOccupancy(realtimeJourney.Occupancy) {
		occupancy := realtimeJourney.Occupancy
		stored.Occupancy = &occupancy
	}
	return stored
}

func storedJourneyFromCTDF(journey *ctdf.Journey, realtimeJourneyID string) *storedJourney {
	if journey == nil {
		return nil
	}

	stored := &storedJourney{
		PrimaryIdentifier:  journey.PrimaryIdentifier,
		ServiceRef:         journey.ServiceRef,
		OperatorRef:        journey.OperatorRef,
		DepartureTimezone:  journey.DepartureTimezone,
		DestinationDisplay: journey.DestinationDisplay,
	}

	if journey.PrimaryIdentifier == realtimeJourneyID {
		stored.Service = storedServiceFromCTDF(journey.Service)
		stored.Operator = storedOperatorFromCTDF(journey.Operator)
	}

	return stored
}

func storedServiceFromCTDF(service *ctdf.Service) *storedService {
	if service == nil {
		return nil
	}

	return &storedService{
		PrimaryIdentifier:    service.PrimaryIdentifier,
		ServiceName:          service.ServiceName,
		OperatorRef:          service.OperatorRef,
		BrandColour:          service.BrandColour,
		SecondaryBrandColour: service.SecondaryBrandColour,
		BrandIcon:            service.BrandIcon,
		BrandDisplayMode:     service.BrandDisplayMode,
		TransportType:        service.TransportType,
	}
}

func storedOperatorFromCTDF(operator *ctdf.Operator) *storedOperator {
	if operator == nil {
		return nil
	}

	return &storedOperator{
		PrimaryIdentifier: operator.PrimaryIdentifier,
		PrimaryName:       operator.PrimaryName,
		TransportType:     operator.TransportType,
	}
}

func storedRealtimeStopsFromCTDF(stops map[string]*ctdf.RealtimeJourneyStops) map[string]*storedRealtimeStop {
	if len(stops) == 0 {
		return nil
	}

	storedStops := make(map[string]*storedRealtimeStop, len(stops))
	for key, stop := range stops {
		if stop == nil {
			continue
		}

		storedStops[key] = &storedRealtimeStop{
			StopRef:       stop.StopRef,
			Platform:      stop.Platform,
			ArrivalTime:   stop.ArrivalTime,
			DepartureTime: stop.DepartureTime,
			TimeType:      stop.TimeType,
			Cancelled:     stop.Cancelled,
		}
	}

	return storedStops
}

func (stored *storedRealtimeJourney) toCTDF(ctx context.Context, hydrateJourney bool) *ctdf.RealtimeJourney {
	if stored == nil {
		return nil
	}

	realtimeJourney := &ctdf.RealtimeJourney{
		PrimaryIdentifier:          stored.PrimaryIdentifier,
		OtherIdentifiers:           stored.OtherIdentifiers,
		ActivelyTracked:            stored.ActivelyTracked,
		Journey:                    stored.Journey.toCTDF(),
		Service:                    stored.Service.toCTDF(),
		JourneyRunDate:             stored.JourneyRunDate,
		CreationDateTime:           stored.CreationDateTime,
		ModificationDateTime:       stored.ModificationDateTime,
		TimeoutDurationMinutes:     stored.TimeoutDurationMinutes,
		DataSource:                 stored.DataSource,
		VehicleLocationDescription: stored.VehicleLocationDesc,
		VehicleBearing:             stored.VehicleBearing,
		DepartedStopRef:            stored.DepartedStopRef,
		NextStopRef:                stored.NextStopRef,
		Stops:                      stored.realtimeStops(),
		Offset:                     stored.Offset,
		Reliability:                stored.Reliability,
		VehicleRef:                 stored.VehicleRef,
		Cancelled:                  stored.Cancelled,
	}

	if stored.VehicleLocation != nil {
		realtimeJourney.VehicleLocation = *stored.VehicleLocation
	}
	if stored.Occupancy != nil {
		realtimeJourney.Occupancy = *stored.Occupancy
	}
	if stored.DetailedRail != nil {
		realtimeJourney.DetailedRailInformation = *stored.DetailedRail
	}
	if realtimeJourney.Journey != nil && realtimeJourney.Journey.Service == nil {
		realtimeJourney.Journey.Service = realtimeJourney.Service
	}
	if hydrateJourney {
		LoadStaticJourney(ctx, realtimeJourney)
	}

	return realtimeJourney
}

func (stored *storedJourney) toCTDF() *ctdf.Journey {
	if stored == nil {
		return nil
	}

	return &ctdf.Journey{
		PrimaryIdentifier:  stored.PrimaryIdentifier,
		ServiceRef:         stored.ServiceRef,
		Service:            stored.Service.toCTDF(),
		OperatorRef:        stored.OperatorRef,
		Operator:           stored.Operator.toCTDF(),
		DepartureTimezone:  stored.DepartureTimezone,
		DestinationDisplay: stored.DestinationDisplay,
	}
}

func (stored *storedService) toCTDF() *ctdf.Service {
	if stored == nil {
		return nil
	}

	return &ctdf.Service{
		PrimaryIdentifier:    stored.PrimaryIdentifier,
		ServiceName:          stored.ServiceName,
		OperatorRef:          stored.OperatorRef,
		BrandColour:          stored.BrandColour,
		SecondaryBrandColour: stored.SecondaryBrandColour,
		BrandIcon:            stored.BrandIcon,
		BrandDisplayMode:     stored.BrandDisplayMode,
		TransportType:        stored.TransportType,
	}
}

func (stored *storedOperator) toCTDF() *ctdf.Operator {
	if stored == nil {
		return nil
	}

	return &ctdf.Operator{
		PrimaryIdentifier: stored.PrimaryIdentifier,
		PrimaryName:       stored.PrimaryName,
		TransportType:     stored.TransportType,
	}
}

func (stored *storedRealtimeJourney) realtimeStops() map[string]*ctdf.RealtimeJourneyStops {
	if len(stored.Stops) == 0 {
		return map[string]*ctdf.RealtimeJourneyStops{}
	}

	stops := make(map[string]*ctdf.RealtimeJourneyStops, len(stored.Stops))
	for key, stop := range stored.Stops {
		if stop == nil {
			continue
		}

		stops[key] = &ctdf.RealtimeJourneyStops{
			StopRef:       stop.StopRef,
			Platform:      stop.Platform,
			ArrivalTime:   stop.ArrivalTime,
			DepartureTime: stop.DepartureTime,
			TimeType:      stop.TimeType,
			Cancelled:     stop.Cancelled,
		}
	}

	return stops
}

func LoadStaticJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	if realtimeJourney == nil || realtimeJourney.Journey == nil {
		return
	}
	if realtimeJourney.Journey.PrimaryIdentifier == "" || realtimeJourney.Journey.PrimaryIdentifier == realtimeJourney.PrimaryIdentifier {
		return
	}

	var journey ctdf.Journey
	err := database.GetCollection("journeys").FindOne(ctx, bson.M{"primaryidentifier": realtimeJourney.Journey.PrimaryIdentifier}).Decode(&journey)
	if err != nil {
		return
	}

	realtimeJourney.Journey = &journey
	if realtimeJourney.Service != nil {
		realtimeJourney.Journey.Service = realtimeJourney.Service
	}
}

func decodeStoredRealtimeJourney(ctx context.Context, data []byte, hydrateJourney bool) (*ctdf.RealtimeJourney, error) {
	var stored storedRealtimeJourney
	if err := json.Unmarshal(data, &stored); err == nil && stored.PrimaryIdentifier != "" {
		return stored.toCTDF(ctx, hydrateJourney), nil
	}

	var realtimeJourney ctdf.RealtimeJourney
	if err := json.Unmarshal(data, &realtimeJourney); err != nil {
		return nil, err
	}
	if hydrateJourney {
		LoadStaticJourney(ctx, &realtimeJourney)
	}

	return &realtimeJourney, nil
}

func hasOccupancy(occupancy ctdf.RealtimeJourneyOccupancy) bool {
	return occupancy.OccupancyAvailable ||
		occupancy.ActualValues ||
		occupancy.WheelchairInformation ||
		occupancy.SeatedInformation ||
		occupancy.TotalPercentageOccupancy != 0 ||
		occupancy.Capacity != 0 ||
		occupancy.SeatedCapacity != 0 ||
		occupancy.WheelchairCapacity != 0 ||
		occupancy.Occupancy != 0 ||
		occupancy.SeatedOccupancy != 0 ||
		occupancy.WheelchairOccupancy != 0
}

func realtimeJourneyIdentifierFromDetailsKey(key string) string {
	return strings.TrimPrefix(key, realtimeJourneyDetailsKey(""))
}
