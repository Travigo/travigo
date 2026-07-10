package vehicletracker

import (
	"context"
	"sort"
	"time"

	"github.com/travigo/travigo/pkg/database"
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
	if event == nil || event.VehicleLocationUpdate == nil || event.VehicleLocationUpdate.Location.Type != "Point" {
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

	cursor, err := database.GetCollection("journeys").Find(context.Background(), bson.M{"serviceref": bson.M{"$in": serviceIDs}}, options.Find().SetProjection(bson.M{"primaryidentifier": 1, "availability": 1}).SetLimit(maxLocationCandidates))
	if err != nil {
		return ""
	}
	defer cursor.Close(context.Background())

	candidates := make([]locationJourneyCandidate, 0, 8)
	history := loadVehicleJourneyHistory(context.Background(), event)
	for cursor.Next(context.Background()) {
		var row struct {
			PrimaryIdentifier string `bson:"primaryidentifier"`
		}
		if err := cursor.Decode(&row); err != nil || row.PrimaryIdentifier == "" {
			continue
		}
		journey, err := consumer.getCachedTrackedJourney(row.PrimaryIdentifier, event.RecordedAt)
		if err != nil || journey.Journey == nil || (journey.Journey.Availability != nil && !journey.Journey.Availability.MatchDate(serviceDate)) {
			continue
		}
		match, ok := matchJourneyPosition(journey.Journey, event.VehicleLocationUpdate.Location)
		if ok && match.DistanceMetres <= locationCandidateDistanceMetres {
			candidates = append(candidates, locationJourneyCandidate{journeyID: row.PrimaryIdentifier, distance: match.DistanceMetres, score: scoreLocationCandidate(match.DistanceMetres, match.JourneyProgress, row.PrimaryIdentifier, history)})
		}
	}
	return selectLocationCandidate(candidates)
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
