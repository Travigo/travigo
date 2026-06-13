package realtimestore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

const (
	ActiveJourneyTTL   = 4 * time.Hour
	JourneyLookupTTL   = 2 * time.Hour
	JourneySnapshotTTL = 6 * time.Hour

	ambiguousLookupValue = "AMBIGUOUS"

	activeJourneyPrefix          = "rt:journey:"
	journeySnapshotPrefix        = "rt:journey-snapshot:"
	activeJourneyGeoKey          = "rt:geo"
	activeJourneyZSetKey         = "rt:active"
	activeIndexPruneInterval     = time.Minute
	maxActiveIndexPruneBatchSize = 5000
	maxSnapshotTrackPoints       = 96
	realtimeStateVersion         = 1
	journeySnapshotVersion       = 1
)

var ErrNotFound = errors.New("not found")

var lastActiveIndexPruneUnix atomic.Int64

type Stats struct {
	ActiveJourneyIndexCount int64 `json:"active_journey_index_count"`
	GeoIndexCount           int64 `json:"geo_index_count"`
}

type realtimeJourneyState struct {
	Version                int                                  `json:"v"`
	PrimaryIdentifier      string                               `json:"i"`
	OtherIdentifiers       map[string]string                    `json:"oi,omitempty"`
	ActivelyTracked        bool                                 `json:"a,omitempty"`
	JourneyID              string                               `json:"j,omitempty"`
	ServiceID              string                               `json:"s,omitempty"`
	JourneyRunDate         time.Time                            `json:"rd,omitempty"`
	CreationDateTime       time.Time                            `json:"cd,omitempty"`
	ModificationDateTime   time.Time                            `json:"md,omitempty"`
	TimeoutDurationMinutes int                                  `json:"to,omitempty"`
	DataSource             *ctdf.DataSourceReference            `json:"ds,omitempty"`
	VehicleLocation        *ctdf.Location                       `json:"l,omitempty"`
	VehicleLocationDesc    string                               `json:"ld,omitempty"`
	VehicleBearing         float64                              `json:"b,omitempty"`
	DepartedStopRef        string                               `json:"dp,omitempty"`
	NextStopRef            string                               `json:"ns,omitempty"`
	Stops                  map[string]*realtimeJourneyStopState `json:"st,omitempty"`
	Offset                 time.Duration                        `json:"of,omitempty"`
	Reliability            ctdf.RealtimeJourneyReliabilityType  `json:"r,omitempty"`
	VehicleRef             string                               `json:"vr,omitempty"`
	Cancelled              bool                                 `json:"c,omitempty"`
	Occupancy              *realtimeJourneyOccupancyState       `json:"o,omitempty"`
	DetailedRail           *ctdf.RealtimeJourneyDetailedRail    `json:"dr,omitempty"`
}

type realtimeJourneyStopState struct {
	StopRef       string                           `json:"s,omitempty"`
	Platform      string                           `json:"p,omitempty"`
	ArrivalTime   time.Time                        `json:"a,omitempty"`
	DepartureTime time.Time                        `json:"d,omitempty"`
	TimeType      ctdf.RealtimeJourneyStopTimeType `json:"t,omitempty"`
	Cancelled     bool                             `json:"c,omitempty"`
}

type realtimeJourneyOccupancyState struct {
	OccupancyAvailable       bool `json:"a,omitempty"`
	ActualValues             bool `json:"av,omitempty"`
	WheelchairInformation    bool `json:"w,omitempty"`
	SeatedInformation        bool `json:"si,omitempty"`
	TotalPercentageOccupancy int  `json:"tp,omitempty"`
	Capacity                 int  `json:"ca,omitempty"`
	SeatedCapacity           int  `json:"sc,omitempty"`
	WheelchairCapacity       int  `json:"wc,omitempty"`
	Occupancy                int  `json:"o,omitempty"`
	SeatedOccupancy          int  `json:"so,omitempty"`
	WheelchairOccupancy      int  `json:"wo,omitempty"`
}

type journeySnapshot struct {
	Version            int                    `json:"v"`
	PrimaryIdentifier  string                 `json:"i"`
	OtherIdentifiers   map[string]string      `json:"oi,omitempty"`
	ServiceRef         string                 `json:"sr,omitempty"`
	Service            *serviceSnapshot       `json:"s,omitempty"`
	OperatorRef        string                 `json:"or,omitempty"`
	Operator           *operatorSnapshot      `json:"o,omitempty"`
	Direction          string                 `json:"di,omitempty"`
	DepartureTime      time.Time              `json:"dt,omitempty"`
	DepartureTimezone  string                 `json:"tz,omitempty"`
	DestinationDisplay string                 `json:"dd,omitempty"`
	Path               []*journeyPathSnapshot `json:"p,omitempty"`
}

type serviceSnapshot struct {
	PrimaryIdentifier    string             `json:"i,omitempty"`
	ServiceName          string             `json:"n,omitempty"`
	OperatorRef          string             `json:"o,omitempty"`
	BrandColour          string             `json:"bc,omitempty"`
	SecondaryBrandColour string             `json:"sc,omitempty"`
	BrandIcon            string             `json:"bi,omitempty"`
	BrandDisplayMode     string             `json:"bd,omitempty"`
	TransportType        ctdf.TransportType `json:"tt,omitempty"`
}

type operatorSnapshot struct {
	PrimaryIdentifier string             `json:"i,omitempty"`
	PrimaryName       string             `json:"n,omitempty"`
	TransportType     ctdf.TransportType `json:"tt,omitempty"`
}

type journeyPathSnapshot struct {
	OriginStopRef          string                         `json:"os,omitempty"`
	DestinationStopRef     string                         `json:"ds,omitempty"`
	OriginStop             *stopSnapshot                  `json:"osp,omitempty"`
	DestinationStop        *stopSnapshot                  `json:"dsp,omitempty"`
	OriginPlatform         string                         `json:"op,omitempty"`
	DestinationPlatform    string                         `json:"dp,omitempty"`
	Distance               int                            `json:"d,omitempty"`
	OriginArrivalTime      time.Time                      `json:"oa,omitempty"`
	DestinationArrivalTime time.Time                      `json:"da,omitempty"`
	OriginDepartureTime    time.Time                      `json:"od,omitempty"`
	DestinationDisplay     string                         `json:"dd,omitempty"`
	OriginActivity         []ctdf.JourneyPathItemActivity `json:"oac,omitempty"`
	DestinationActivity    []ctdf.JourneyPathItemActivity `json:"dac,omitempty"`
	Track                  []ctdf.Location                `json:"t,omitempty"`
}

type stopSnapshot struct {
	PrimaryIdentifier string         `json:"i,omitempty"`
	PrimaryName       string         `json:"n,omitempty"`
	Timezone          string         `json:"tz,omitempty"`
	Location          *ctdf.Location `json:"l,omitempty"`
}

func keyPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}

	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func ActiveJourneyKey(identifier string) string {
	return activeJourneyPrefix + keyPart(identifier)
}

func JourneySnapshotKey(journeyID string) string {
	return journeySnapshotPrefix + keyPart(journeyID)
}

func GTFSJourneyLookupKey(linkedDataset string, tripID string) string {
	if linkedDataset == "" || tripID == "" {
		return ""
	}

	return fmt.Sprintf("lookup:journey:gtfs:%s:%s", keyPart(linkedDataset), keyPart(tripID))
}

func GTFSAnyJourneyLookupKey(tripID string) string {
	if tripID == "" {
		return ""
	}

	return fmt.Sprintf("lookup:journey:gtfs-any:%s", keyPart(tripID))
}

func SIRIJourneyRefLookupKey(operatorRef string, lineRef string, vehicleJourneyRef string) string {
	if operatorRef == "" || lineRef == "" || vehicleJourneyRef == "" {
		return ""
	}

	return fmt.Sprintf("lookup:journey:siri:journeyref:%s:%s:%s", keyPart(operatorRef), keyPart(lineRef), keyPart(vehicleJourneyRef))
}

func SIRIBlockLookupKey(operatorRef string, lineRef string, blockRef string) string {
	if operatorRef == "" || lineRef == "" || blockRef == "" {
		return ""
	}

	return fmt.Sprintf("lookup:journey:siri:block:%s:%s:%s", keyPart(operatorRef), keyPart(lineRef), keyPart(blockRef))
}

func SIRIOriginDestinationTimeLookupKey(operatorRef string, lineRef string, originRef string, destinationRef string, departureHHMM string) string {
	if operatorRef == "" || lineRef == "" || originRef == "" || destinationRef == "" || departureHHMM == "" {
		return ""
	}

	return fmt.Sprintf(
		"lookup:journey:siri:odt:%s:%s:%s:%s:%s",
		keyPart(operatorRef),
		keyPart(lineRef),
		keyPart(originRef),
		keyPart(destinationRef),
		keyPart(departureHHMM),
	)
}

func SetJourneyLookup(ctx context.Context, key string, journeyID string) {
	if redis_client.Client == nil || key == "" || journeyID == "" {
		return
	}

	created, err := redis_client.Client.SetNX(ctx, key, journeyID, JourneyLookupTTL).Result()
	if err != nil {
		return
	}
	if created {
		return
	}

	currentValue, err := redis_client.Client.Get(ctx, key).Result()
	if err == nil && currentValue != "" && currentValue != journeyID {
		redis_client.Client.Set(ctx, key, ambiguousLookupValue, JourneyLookupTTL)
		return
	}

	if err != nil && err != redis.Nil {
		return
	}

	redis_client.Client.Set(ctx, key, journeyID, JourneyLookupTTL)
}

func GetJourneyLookup(ctx context.Context, keys ...string) (string, error) {
	if redis_client.Client == nil {
		return "", ErrNotFound
	}

	for _, key := range keys {
		if key == "" {
			continue
		}

		value, err := redis_client.Client.Get(ctx, key).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			continue
		}
		if value == "" || value == ambiguousLookupValue {
			continue
		}

		return value, nil
	}

	return "", ErrNotFound
}

func GetStats(ctx context.Context) (Stats, error) {
	stats := Stats{}
	if redis_client.Client == nil {
		return stats, ErrNotFound
	}

	activeCount, err := redis_client.Client.ZCard(ctx, activeJourneyZSetKey).Result()
	if err != nil {
		return stats, err
	}

	geoCount, err := redis_client.Client.ZCard(ctx, activeJourneyGeoKey).Result()
	if err != nil && err != redis.Nil {
		return stats, err
	}

	stats.ActiveJourneyIndexCount = activeCount
	stats.GeoIndexCount = geoCount

	return stats, nil
}

func SetRealtimeJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	if redis_client.Client == nil || realtimeJourney == nil || realtimeJourney.PrimaryIdentifier == "" {
		return
	}

	if realtimeJourney.Journey != nil {
		setJourneySnapshot(ctx, realtimeJourney.Journey, true)
	}

	realtimeJourneyJSON, err := json.Marshal(newRealtimeJourneyState(realtimeJourney))
	if err != nil {
		return
	}

	identifier := realtimeJourney.PrimaryIdentifier
	pipe := redis_client.Client.Pipeline()
	pipe.Set(ctx, ActiveJourneyKey(identifier), realtimeJourneyJSON, ActiveJourneyTTL)
	pipe.Del(ctx, identifier)

	pipe.ZAdd(ctx, activeJourneyZSetKey, redis.Z{
		Score:  float64(realtimeJourney.ModificationDateTime.Unix()),
		Member: identifier,
	})

	if realtimeJourney.VehicleLocation.Type == "Point" && len(realtimeJourney.VehicleLocation.Coordinates) == 2 {
		pipe.GeoAdd(ctx, activeJourneyGeoKey, &redis.GeoLocation{
			Name:      identifier,
			Longitude: realtimeJourney.VehicleLocation.Coordinates[0],
			Latitude:  realtimeJourney.VehicleLocation.Coordinates[1],
		})
	} else {
		pipe.ZRem(ctx, activeJourneyGeoKey, identifier)
	}

	pipe.Exec(ctx)
	maybePruneActiveJourneyIndexes(ctx)
}

func GetRealtimeJourney(ctx context.Context, identifier string) (*ctdf.RealtimeJourney, error) {
	if redis_client.Client == nil || identifier == "" {
		return nil, ErrNotFound
	}

	for _, key := range []string{ActiveJourneyKey(identifier), identifier} {
		realtimeJourneyJSON, err := redis_client.Client.Get(ctx, key).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			continue
		}

		realtimeJourney, err := decodeRealtimeJourney(ctx, []byte(realtimeJourneyJSON))
		if err != nil {
			continue
		}

		return realtimeJourney, nil
	}

	return nil, ErrNotFound
}

func SetJourneySnapshot(ctx context.Context, journey *ctdf.Journey) {
	setJourneySnapshot(ctx, journey, false)
}

func setJourneySnapshot(ctx context.Context, journey *ctdf.Journey, onlyIfMissing bool) {
	if redis_client.Client == nil || journey == nil || journey.PrimaryIdentifier == "" {
		return
	}

	journeyJSON, err := json.Marshal(newJourneySnapshot(journey))
	if err != nil {
		return
	}

	if onlyIfMissing {
		redis_client.Client.SetNX(ctx, JourneySnapshotKey(journey.PrimaryIdentifier), journeyJSON, JourneySnapshotTTL)
		return
	}

	redis_client.Client.Set(ctx, JourneySnapshotKey(journey.PrimaryIdentifier), journeyJSON, JourneySnapshotTTL)
}

func GetJourneySnapshot(ctx context.Context, journeyID string) (*ctdf.Journey, error) {
	if redis_client.Client == nil || journeyID == "" {
		return nil, ErrNotFound
	}

	journeyJSON, err := redis_client.Client.Get(ctx, JourneySnapshotKey(journeyID)).Result()
	if err == redis.Nil {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var snapshotMarker struct {
		Version           int    `json:"v"`
		PrimaryIdentifier string `json:"i"`
	}
	if err := json.Unmarshal([]byte(journeyJSON), &snapshotMarker); err == nil && snapshotMarker.PrimaryIdentifier != "" {
		var snapshot journeySnapshot
		if err := json.Unmarshal([]byte(journeyJSON), &snapshot); err != nil {
			return nil, err
		}

		return snapshot.toJourney(), nil
	}

	var journey ctdf.Journey
	if err := json.Unmarshal([]byte(journeyJSON), &journey); err != nil {
		return nil, err
	}

	return &journey, nil
}

func decodeRealtimeJourney(ctx context.Context, payload []byte) (*ctdf.RealtimeJourney, error) {
	var stateMarker struct {
		Version           int    `json:"v"`
		PrimaryIdentifier string `json:"i"`
	}
	if err := json.Unmarshal(payload, &stateMarker); err == nil && stateMarker.PrimaryIdentifier != "" {
		var state realtimeJourneyState
		if err := json.Unmarshal(payload, &state); err != nil {
			return nil, err
		}

		return state.toRealtimeJourney(ctx), nil
	}

	var realtimeJourney ctdf.RealtimeJourney
	if err := json.Unmarshal(payload, &realtimeJourney); err != nil {
		return nil, err
	}

	if realtimeJourney.Journey != nil {
		setJourneySnapshot(ctx, realtimeJourney.Journey, true)
	}

	return &realtimeJourney, nil
}

func newRealtimeJourneyState(realtimeJourney *ctdf.RealtimeJourney) *realtimeJourneyState {
	journeyID := inferJourneyID(realtimeJourney)
	serviceID := ""
	if realtimeJourney.Service != nil {
		serviceID = realtimeJourney.Service.PrimaryIdentifier
	}
	if serviceID == "" && realtimeJourney.Journey != nil {
		serviceID = realtimeJourney.Journey.ServiceRef
	}

	state := &realtimeJourneyState{
		Version:                realtimeStateVersion,
		PrimaryIdentifier:      realtimeJourney.PrimaryIdentifier,
		OtherIdentifiers:       realtimeJourney.OtherIdentifiers,
		ActivelyTracked:        realtimeJourney.ActivelyTracked,
		JourneyID:              journeyID,
		ServiceID:              serviceID,
		JourneyRunDate:         realtimeJourney.JourneyRunDate,
		CreationDateTime:       realtimeJourney.CreationDateTime,
		ModificationDateTime:   realtimeJourney.ModificationDateTime,
		TimeoutDurationMinutes: realtimeJourney.TimeoutDurationMinutes,
		DataSource:             realtimeJourney.DataSource,
		VehicleLocation:        locationState(realtimeJourney.VehicleLocation),
		VehicleLocationDesc:    realtimeJourney.VehicleLocationDescription,
		VehicleBearing:         realtimeJourney.VehicleBearing,
		DepartedStopRef:        realtimeJourney.DepartedStopRef,
		NextStopRef:            realtimeJourney.NextStopRef,
		Stops:                  realtimeStopStates(realtimeJourney.Stops),
		Offset:                 realtimeJourney.Offset,
		Reliability:            realtimeJourney.Reliability,
		VehicleRef:             realtimeJourney.VehicleRef,
		Cancelled:              realtimeJourney.Cancelled,
		Occupancy:              occupancyState(realtimeJourney.Occupancy),
	}

	if len(realtimeJourney.DetailedRailInformation.Carriages) > 0 {
		state.DetailedRail = &realtimeJourney.DetailedRailInformation
	}

	return state
}

func (state *realtimeJourneyState) toRealtimeJourney(ctx context.Context) *ctdf.RealtimeJourney {
	realtimeJourney := &ctdf.RealtimeJourney{
		PrimaryIdentifier:          state.PrimaryIdentifier,
		OtherIdentifiers:           state.OtherIdentifiers,
		ActivelyTracked:            state.ActivelyTracked,
		JourneyRunDate:             state.JourneyRunDate,
		CreationDateTime:           state.CreationDateTime,
		ModificationDateTime:       state.ModificationDateTime,
		TimeoutDurationMinutes:     state.TimeoutDurationMinutes,
		DataSource:                 state.DataSource,
		VehicleLocation:            dereferenceLocation(state.VehicleLocation),
		VehicleLocationDescription: state.VehicleLocationDesc,
		VehicleBearing:             state.VehicleBearing,
		DepartedStopRef:            state.DepartedStopRef,
		NextStopRef:                state.NextStopRef,
		Stops:                      state.toRealtimeStops(),
		Offset:                     state.Offset,
		Reliability:                state.Reliability,
		VehicleRef:                 state.VehicleRef,
		Cancelled:                  state.Cancelled,
		Occupancy:                  state.toOccupancy(),
	}

	if state.DetailedRail != nil {
		realtimeJourney.DetailedRailInformation = *state.DetailedRail
	}

	if state.JourneyID != "" {
		if journey, err := GetJourneySnapshot(ctx, state.JourneyID); err == nil {
			realtimeJourney.Journey = journey
			realtimeJourney.Service = journey.Service
		}
	}

	if realtimeJourney.Service == nil && state.ServiceID != "" {
		realtimeJourney.Service = &ctdf.Service{PrimaryIdentifier: state.ServiceID}
	}

	if realtimeJourney.DepartedStopRef != "" && realtimeJourney.Journey != nil {
		realtimeJourney.DepartedStop = findPathStop(realtimeJourney.Journey, realtimeJourney.DepartedStopRef)
	}
	if realtimeJourney.NextStopRef != "" && realtimeJourney.Journey != nil {
		realtimeJourney.NextStop = findPathStop(realtimeJourney.Journey, realtimeJourney.NextStopRef)
	}

	return realtimeJourney
}

func inferJourneyID(realtimeJourney *ctdf.RealtimeJourney) string {
	if realtimeJourney == nil {
		return ""
	}
	if realtimeJourney.Journey != nil && realtimeJourney.Journey.PrimaryIdentifier != "" {
		return realtimeJourney.Journey.PrimaryIdentifier
	}
	if index := strings.Index(realtimeJourney.PrimaryIdentifier, ":"); index >= 0 && index < len(realtimeJourney.PrimaryIdentifier)-1 {
		return realtimeJourney.PrimaryIdentifier[index+1:]
	}
	return ""
}

func locationState(location ctdf.Location) *ctdf.Location {
	if location.Type == "" && len(location.Coordinates) == 0 {
		return nil
	}

	return &ctdf.Location{
		Type:        location.Type,
		Coordinates: append([]float64(nil), location.Coordinates...),
	}
}

func dereferenceLocation(location *ctdf.Location) ctdf.Location {
	if location == nil {
		return ctdf.Location{}
	}

	return *location
}

func realtimeStopStates(stops map[string]*ctdf.RealtimeJourneyStops) map[string]*realtimeJourneyStopState {
	if len(stops) == 0 {
		return nil
	}

	states := map[string]*realtimeJourneyStopState{}
	for key, stop := range stops {
		if stop == nil {
			continue
		}

		states[key] = &realtimeJourneyStopState{
			StopRef:       stop.StopRef,
			Platform:      stop.Platform,
			ArrivalTime:   stop.ArrivalTime,
			DepartureTime: stop.DepartureTime,
			TimeType:      stop.TimeType,
			Cancelled:     stop.Cancelled,
		}
	}

	return states
}

func (state *realtimeJourneyState) toRealtimeStops() map[string]*ctdf.RealtimeJourneyStops {
	if len(state.Stops) == 0 {
		return nil
	}

	stops := map[string]*ctdf.RealtimeJourneyStops{}
	for key, stop := range state.Stops {
		if stop == nil {
			continue
		}

		stopRef := stop.StopRef
		if stopRef == "" {
			stopRef = key
		}

		stops[key] = &ctdf.RealtimeJourneyStops{
			StopRef:       stopRef,
			Platform:      stop.Platform,
			ArrivalTime:   stop.ArrivalTime,
			DepartureTime: stop.DepartureTime,
			TimeType:      stop.TimeType,
			Cancelled:     stop.Cancelled,
		}
	}

	return stops
}

func occupancyState(occupancy ctdf.RealtimeJourneyOccupancy) *realtimeJourneyOccupancyState {
	if occupancy == (ctdf.RealtimeJourneyOccupancy{}) {
		return nil
	}

	return &realtimeJourneyOccupancyState{
		OccupancyAvailable:       occupancy.OccupancyAvailable,
		ActualValues:             occupancy.ActualValues,
		WheelchairInformation:    occupancy.WheelchairInformation,
		SeatedInformation:        occupancy.SeatedInformation,
		TotalPercentageOccupancy: occupancy.TotalPercentageOccupancy,
		Capacity:                 occupancy.Capacity,
		SeatedCapacity:           occupancy.SeatedCapacity,
		WheelchairCapacity:       occupancy.WheelchairCapacity,
		Occupancy:                occupancy.Occupancy,
		SeatedOccupancy:          occupancy.SeatedOccupancy,
		WheelchairOccupancy:      occupancy.WheelchairOccupancy,
	}
}

func (state *realtimeJourneyState) toOccupancy() ctdf.RealtimeJourneyOccupancy {
	if state.Occupancy == nil {
		return ctdf.RealtimeJourneyOccupancy{}
	}

	return ctdf.RealtimeJourneyOccupancy{
		OccupancyAvailable:       state.Occupancy.OccupancyAvailable,
		ActualValues:             state.Occupancy.ActualValues,
		WheelchairInformation:    state.Occupancy.WheelchairInformation,
		SeatedInformation:        state.Occupancy.SeatedInformation,
		TotalPercentageOccupancy: state.Occupancy.TotalPercentageOccupancy,
		Capacity:                 state.Occupancy.Capacity,
		SeatedCapacity:           state.Occupancy.SeatedCapacity,
		WheelchairCapacity:       state.Occupancy.WheelchairCapacity,
		Occupancy:                state.Occupancy.Occupancy,
		SeatedOccupancy:          state.Occupancy.SeatedOccupancy,
		WheelchairOccupancy:      state.Occupancy.WheelchairOccupancy,
	}
}

func newJourneySnapshot(journey *ctdf.Journey) *journeySnapshot {
	snapshot := &journeySnapshot{
		Version:            journeySnapshotVersion,
		PrimaryIdentifier:  journey.PrimaryIdentifier,
		OtherIdentifiers:   journey.OtherIdentifiers,
		ServiceRef:         journey.ServiceRef,
		Service:            newServiceSnapshot(journey.Service),
		OperatorRef:        journey.OperatorRef,
		Operator:           newOperatorSnapshot(journey.Operator),
		Direction:          journey.Direction,
		DepartureTime:      journey.DepartureTime,
		DepartureTimezone:  journey.DepartureTimezone,
		DestinationDisplay: journey.DestinationDisplay,
	}

	if len(journey.Path) > 0 {
		snapshot.Path = make([]*journeyPathSnapshot, 0, len(journey.Path))
		for _, path := range journey.Path {
			if path == nil {
				continue
			}

			snapshot.Path = append(snapshot.Path, &journeyPathSnapshot{
				OriginStopRef:          path.OriginStopRef,
				DestinationStopRef:     path.DestinationStopRef,
				OriginStop:             newStopSnapshot(path.OriginStop),
				DestinationStop:        newStopSnapshot(path.DestinationStop),
				OriginPlatform:         path.OriginPlatform,
				DestinationPlatform:    path.DestinationPlatform,
				Distance:               path.Distance,
				OriginArrivalTime:      path.OriginArrivalTime,
				DestinationArrivalTime: path.DestinationArrivalTime,
				OriginDepartureTime:    path.OriginDepartureTime,
				DestinationDisplay:     path.DestinationDisplay,
				OriginActivity:         path.OriginActivity,
				DestinationActivity:    path.DestinationActivity,
				Track:                  compactTrack(path.Track),
			})
		}
	}

	return snapshot
}

func (snapshot *journeySnapshot) toJourney() *ctdf.Journey {
	journey := &ctdf.Journey{
		PrimaryIdentifier:  snapshot.PrimaryIdentifier,
		OtherIdentifiers:   snapshot.OtherIdentifiers,
		ServiceRef:         snapshot.ServiceRef,
		Service:            snapshot.Service.toService(),
		OperatorRef:        snapshot.OperatorRef,
		Operator:           snapshot.Operator.toOperator(),
		Direction:          snapshot.Direction,
		DepartureTime:      snapshot.DepartureTime,
		DepartureTimezone:  snapshot.DepartureTimezone,
		DestinationDisplay: snapshot.DestinationDisplay,
	}

	if len(snapshot.Path) > 0 {
		journey.Path = make([]*ctdf.JourneyPathItem, 0, len(snapshot.Path))
		for _, path := range snapshot.Path {
			if path == nil {
				continue
			}

			journey.Path = append(journey.Path, &ctdf.JourneyPathItem{
				OriginStopRef:          path.OriginStopRef,
				DestinationStopRef:     path.DestinationStopRef,
				OriginStop:             path.OriginStop.toStop(),
				DestinationStop:        path.DestinationStop.toStop(),
				OriginPlatform:         path.OriginPlatform,
				DestinationPlatform:    path.DestinationPlatform,
				Distance:               path.Distance,
				OriginArrivalTime:      path.OriginArrivalTime,
				DestinationArrivalTime: path.DestinationArrivalTime,
				OriginDepartureTime:    path.OriginDepartureTime,
				DestinationDisplay:     path.DestinationDisplay,
				OriginActivity:         path.OriginActivity,
				DestinationActivity:    path.DestinationActivity,
				Track:                  path.Track,
			})
		}
	}

	return journey
}

func newServiceSnapshot(service *ctdf.Service) *serviceSnapshot {
	if service == nil {
		return nil
	}

	return &serviceSnapshot{
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

func (snapshot *serviceSnapshot) toService() *ctdf.Service {
	if snapshot == nil {
		return nil
	}

	return &ctdf.Service{
		PrimaryIdentifier:    snapshot.PrimaryIdentifier,
		ServiceName:          snapshot.ServiceName,
		OperatorRef:          snapshot.OperatorRef,
		BrandColour:          snapshot.BrandColour,
		SecondaryBrandColour: snapshot.SecondaryBrandColour,
		BrandIcon:            snapshot.BrandIcon,
		BrandDisplayMode:     snapshot.BrandDisplayMode,
		TransportType:        snapshot.TransportType,
	}
}

func newOperatorSnapshot(operator *ctdf.Operator) *operatorSnapshot {
	if operator == nil {
		return nil
	}

	return &operatorSnapshot{
		PrimaryIdentifier: operator.PrimaryIdentifier,
		PrimaryName:       operator.PrimaryName,
		TransportType:     operator.TransportType,
	}
}

func (snapshot *operatorSnapshot) toOperator() *ctdf.Operator {
	if snapshot == nil {
		return nil
	}

	return &ctdf.Operator{
		PrimaryIdentifier: snapshot.PrimaryIdentifier,
		PrimaryName:       snapshot.PrimaryName,
		TransportType:     snapshot.TransportType,
	}
}

func newStopSnapshot(stop *ctdf.Stop) *stopSnapshot {
	if stop == nil {
		return nil
	}

	return &stopSnapshot{
		PrimaryIdentifier: stop.PrimaryIdentifier,
		PrimaryName:       stop.PrimaryName,
		Timezone:          stop.Timezone,
		Location:          copyLocation(stop.Location),
	}
}

func (snapshot *stopSnapshot) toStop() *ctdf.Stop {
	if snapshot == nil {
		return nil
	}

	return &ctdf.Stop{
		PrimaryIdentifier: snapshot.PrimaryIdentifier,
		PrimaryName:       snapshot.PrimaryName,
		Timezone:          snapshot.Timezone,
		Location:          copyLocation(snapshot.Location),
	}
}

func copyLocation(location *ctdf.Location) *ctdf.Location {
	if location == nil {
		return nil
	}

	return &ctdf.Location{
		Type:        location.Type,
		Coordinates: append([]float64(nil), location.Coordinates...),
	}
}

func compactTrack(track []ctdf.Location) []ctdf.Location {
	if len(track) == 0 {
		return nil
	}
	if len(track) <= maxSnapshotTrackPoints {
		return append([]ctdf.Location(nil), track...)
	}

	compacted := make([]ctdf.Location, 0, maxSnapshotTrackPoints)
	for i := 0; i < maxSnapshotTrackPoints; i++ {
		index := int(math.Round(float64(i*(len(track)-1)) / float64(maxSnapshotTrackPoints-1)))
		compacted = append(compacted, track[index])
	}

	return compacted
}

func findPathStop(journey *ctdf.Journey, stopRef string) *ctdf.Stop {
	if journey == nil || stopRef == "" {
		return nil
	}

	for _, path := range journey.Path {
		if path == nil {
			continue
		}
		if path.OriginStopRef == stopRef && path.OriginStop != nil {
			return path.OriginStop
		}
		if path.DestinationStopRef == stopRef && path.DestinationStop != nil {
			return path.DestinationStop
		}
	}

	return nil
}

func maybePruneActiveJourneyIndexes(ctx context.Context) {
	if redis_client.Client == nil {
		return
	}

	now := time.Now()
	lastPrune := lastActiveIndexPruneUnix.Load()
	if now.Unix()-lastPrune < int64(activeIndexPruneInterval.Seconds()) {
		return
	}
	if !lastActiveIndexPruneUnix.CompareAndSwap(lastPrune, now.Unix()) {
		return
	}

	expiredMembers, err := redis_client.Client.ZRangeByScore(ctx, activeJourneyZSetKey, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    strconv.FormatInt(now.Add(-ActiveJourneyTTL).Unix(), 10),
		Offset: 0,
		Count:  maxActiveIndexPruneBatchSize,
	}).Result()
	if err != nil || len(expiredMembers) == 0 {
		return
	}

	members := make([]interface{}, 0, len(expiredMembers))
	for _, member := range expiredMembers {
		members = append(members, member)
	}

	pipe := redis_client.Client.Pipeline()
	pipe.ZRem(ctx, activeJourneyZSetKey, members...)
	pipe.ZRem(ctx, activeJourneyGeoKey, members...)
	pipe.Exec(ctx)
}
