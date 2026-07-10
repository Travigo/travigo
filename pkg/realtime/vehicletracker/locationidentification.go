package vehicletracker

import (
	"context"
	"sort"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const locationCandidateDistanceMetres = 150
const locationCandidateMarginMetres = 30
const maxLocationCandidates = 100

type locationJourneyCandidate struct {
	journeyID string
	distance  float64
}

func (consumer *BatchConsumer) identifyJourneyFromLocation(event *VehicleUpdateEvent, sourceType string, information map[string]string) string {
	if event == nil || event.VehicleLocationUpdate == nil || event.VehicleLocationUpdate.Location.Type != "Point" {
		return ""
	}
	serviceID := consumer.identifyService(sourceType, information)
	if serviceID == "" {
		return ""
	}
	serviceDate, err := time.Parse("2006-01-02", event.VehicleLocationUpdate.Timeframe)
	if err != nil {
		serviceDate = event.RecordedAt
	}

	cursor, err := database.GetCollection("journeys").Find(context.Background(), bson.M{"serviceref": serviceID}, options.Find().SetProjection(bson.M{"primaryidentifier": 1, "availability": 1}).SetLimit(maxLocationCandidates))
	if err != nil {
		return ""
	}
	defer cursor.Close(context.Background())

	candidates := make([]locationJourneyCandidate, 0, 8)
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
			candidates = append(candidates, locationJourneyCandidate{journeyID: row.PrimaryIdentifier, distance: match.DistanceMetres})
		}
	}
	return selectLocationCandidate(candidates)
}

func selectLocationCandidate(candidates []locationJourneyCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].distance < candidates[j].distance })
	if len(candidates) > 1 && candidates[1].distance-candidates[0].distance < locationCandidateMarginMetres {
		return ""
	}
	return candidates[0].journeyID
}
