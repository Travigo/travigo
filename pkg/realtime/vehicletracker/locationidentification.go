package vehicletracker

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs/nyct"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker/identifiers"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const locationCandidateDistanceMetres = 150
const locationCandidateMarginMetres = 30
const maxLocationCandidates = 100

type locationJourneyCandidate struct {
	journeyID string
	distance  float64
	score     float64
}

func (consumer *BatchConsumer) identifyJourneyFromLocation(event *VehicleUpdateEvent, sourceType string, information map[string]string) string {
	// NYC explicitly permits realtime trips without a static counterpart. A
	// nearby train must not be substituted when the trip key cannot be resolved.
	if sourceType == "GTFS-RT" && information["LinkedDataset"] == nyct.SubwayDataset {
		return ""
	}
	if event == nil || event.VehicleLocationUpdate == nil || event.VehicleLocationUpdate.Location.Type != "Point" || len(event.VehicleLocationUpdate.Location.Coordinates) < 2 {
		return ""
	}
	serviceIDs := consumer.locationCandidateServiceIDs(sourceType, information, event.RecordedAt)
	if len(serviceIDs) == 0 {
		return ""
	}
	serviceDate, err := time.Parse("2006-01-02", event.VehicleLocationUpdate.Timeframe)
	if err != nil {
		serviceDate = event.RecordedAt
	}

	history := loadVehicleJourneyHistory(context.Background(), event)
	return matchLocationJourney(serviceIDs, serviceDate, event.VehicleLocationUpdate.Location, history)
}

func matchLocationJourney(serviceIDs []string, serviceDate time.Time, location ctdf.Location, history *vehicleJourneyHistory) string {
	journeys, err := loadLocationCandidates(serviceIDs, serviceDate)
	if err != nil {
		return ""
	}

	const geometryBatchSize = 16
	candidates := make([]locationJourneyCandidate, 0, 8)
	for batch := range slices.Chunk(journeys, geometryBatchSize) {
		ok, _ := hydrateJourneyTracksWithCache(batch...)
		if !ok {
			// An incomplete database read can hide another plausible match.
			return ""
		}
		for _, journey := range batch {
			match, ok := matchJourneyProgress(journey, location)
			if ok && match.DistanceMetres <= locationCandidateDistanceMetres {
				candidates = append(candidates, locationJourneyCandidate{
					journeyID: journey.PrimaryIdentifier,
					distance:  match.DistanceMetres,
					score:     scoreLocationCandidate(match.DistanceMetres, match.JourneyProgress, journey.PrimaryIdentifier, history),
				})
			}
		}
	}
	return selectLocationCandidate(candidates)
}

func loadLocationCandidates(serviceIDs []string, serviceDate time.Time) ([]*ctdf.Journey, error) {
	started := time.Now()
	defer func() { telemetry.Observe(telemetry.LocationCandidateQuery, time.Since(started)) }()
	ctx := context.Background()
	// Read only what is needed for date filtering and track-based scoring.
	// The winning journey is loaded normally when its realtime update is applied.
	projection := bson.M{
		"_id": 0, "primaryidentifier": 1, "availability": 1, "trackref": 1,
		"calls.stopref": 1, "calls.start": 1, "calls.end": 1, "calls.outgoing.trackref": 1,
	}
	cursor, err := database.GetCollection("journeys").Find(ctx, bson.M{
		"serviceref": bson.M{"$in": serviceIDs},
	}, options.Find().SetProjection(projection).SetLimit(maxLocationCandidates))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var journeys []*ctdf.Journey
	for cursor.Next(ctx) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil || journey.PrimaryIdentifier == "" {
			continue
		}
		if journey.Availability != nil && !journey.Availability.MatchDate(serviceDate) {
			continue
		}
		if len(journey.Calls) == 0 || journey.ValidatePath() != nil {
			continue
		}
		journeys = append(journeys, &journey)
	}
	return journeys, cursor.Err()
}

func (consumer *BatchConsumer) locationCandidateServiceIDs(sourceType string, information map[string]string, observedAt time.Time) []string {
	if sourceType == "siri-vm" {
		identifier := identifiers.SiriVM{IdentifyingInformation: information, CurrentTime: observedAt}
		services, _ := identifier.IdentifyServices()
		return services
	}
	serviceID := consumer.identifyService(sourceType, information)
	if serviceID == "" {
		return nil
	}
	return []string{serviceID}
}

func selectLocationCandidate(candidates []locationJourneyCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score < candidates[j].score })
	if len(candidates) > 1 && candidates[1].score-candidates[0].score < locationCandidateMarginMetres {
		return ""
	}
	return candidates[0].journeyID
}
