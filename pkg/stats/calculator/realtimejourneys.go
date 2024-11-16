package calculator

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type RealtimeJourneyStats struct {
	Current              int64
	LocationWithTrack    int64
	LocationWithoutTrack int64
	ExternalProvided     int64

	NotActivelyTracked int64

	TransportTypes map[ctdf.TransportType]int
	Features       map[string]int
	Datasets       map[string]int
	Providers      map[string]int
	Countries      map[string]int
}

func GetRealtimeJourneys() RealtimeJourneyStats {
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	var numberActiveRealtimeJourneys int64
	var numberActiveRealtimeJourneysWithTrack int64
	var numberActiveRealtimeJourneysWithoutTrack int64
	var numberActiveRealtimeJourneysExternal int64
	var numberActiveRealtimeJourneysNotActivelyTracked int64
	transportTypes := map[ctdf.TransportType]int{}
	features := map[string]int{}
	datasources := map[string]int{}
	providers := map[string]int{}

	realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()

	matchStage := bson.D{
		{
			Key: "$match",
			Value: bson.D{
				{Key: "modificationdatetime", Value: bson.M{"$gt": realtimeActiveCutoffDate}},
				{Key: "activelytracked", Value: true},
			},
		},
	}
	//lookupStage := bson.D{
	//	{
	//		"$lookup",
	//		bson.M{
	//			"from":         "services",
	//			"localField":   "journey.serviceref",
	//			"foreignField": "primaryidentifier",
	//			"as":           "_journeyservice",
	//		},
	//	},
	//}
	//addFieldStage := bson.D{
	//	{
	//		"$addFields",
	//		bson.M{"journey.service": bson.M{"$first": "$_journeyservice"}},
	//	},
	//}
	projectStage := bson.D{
		{
			Key: "$project",
			Value: bson.D{
				bson.E{Key: "primaryidentifier", Value: 1},
				bson.E{Key: "activelytracked", Value: 1},
				bson.E{Key: "timeoutdurationminutes", Value: 1},
				bson.E{Key: "modificationdatetime", Value: 1},
				bson.E{Key: "reliability", Value: 1},
				bson.E{Key: "datasource.datasetid", Value: 1},
				bson.E{Key: "datasource.providerid", Value: 1},
				bson.E{Key: "service.transporttype", Value: 1},
				// bson.E{Key: "vehiclelocation", Value: 1},
				bson.E{Key: "journey.path.destinationstopref", Value: 1},
				bson.E{Key: "journey.path.destinationarrivaltime", Value: 1},
				bson.E{Key: "occupancy.occupancyavailable", Value: 1},
				bson.E{Key: "occupancy.actualvalues", Value: 1},
				bson.E{Key: "detailedrailinformation.carriages.id", Value: 1},
			},
		},
	}
	activeRealtimeJourneys, _ := realtimeJourneysCollection.Aggregate(context.Background(), mongo.Pipeline{matchStage, projectStage})

	for activeRealtimeJourneys.Next(context.Background()) {
		var realtimeJourney ctdf.RealtimeJourney
		err := activeRealtimeJourneys.Decode(&realtimeJourney)

		if err != nil {
			continue
		}

		if realtimeJourney.IsActive() {
			if realtimeJourney.ActivelyTracked {
				numberActiveRealtimeJourneys += 1

				if realtimeJourney.Reliability == ctdf.RealtimeJourneyReliabilityLocationWithTrack {
					numberActiveRealtimeJourneysWithTrack += 1
				}
				if realtimeJourney.Reliability == ctdf.RealtimeJourneyReliabilityLocationWithoutTrack {
					numberActiveRealtimeJourneysWithoutTrack += 1
				}
				if realtimeJourney.Reliability == ctdf.RealtimeJourneyReliabilityExternalProvided {
					numberActiveRealtimeJourneysExternal += 1
				}

				if realtimeJourney.Service != nil {
					transportTypes[realtimeJourney.Service.TransportType] += 1
				}

				datasources[realtimeJourney.DataSource.DatasetID] += 1
				providers[realtimeJourney.DataSource.ProviderID] += 1

				// Features
				if realtimeJourney.Occupancy.OccupancyAvailable {
					features["Occupancy"] += 1

					if realtimeJourney.Occupancy.ActualValues {
						features["OccupancyActualValues"] += 1
					}
				}
				if len(realtimeJourney.DetailedRailInformation.Carriages) > 0 {
					features["DetailedRailInformationCarriages"] += 1
				}
			} else {
				numberActiveRealtimeJourneysNotActivelyTracked += 1
			}
		}
	}

	return RealtimeJourneyStats{
		Current:              numberActiveRealtimeJourneys,
		LocationWithTrack:    numberActiveRealtimeJourneysWithTrack,
		LocationWithoutTrack: numberActiveRealtimeJourneysWithoutTrack,
		ExternalProvided:     numberActiveRealtimeJourneysExternal,
		TransportTypes:       transportTypes,
		Features:             features,
		Datasets:             datasources,
		Providers:            providers,
		Countries:            CountCountries(datasources),
		NotActivelyTracked:   numberActiveRealtimeJourneysNotActivelyTracked,
	}
}
