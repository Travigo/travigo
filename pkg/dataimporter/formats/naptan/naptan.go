package naptan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/transforms"
	"github.com/travigo/travigo/pkg/util"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const DateTimeFormat string = "2006-01-02T15:04:05"

type NaPTAN struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	SchemaVersion string `xml:",attr"`

	StopPoints []*StopPoint
	StopAreas  []*StopArea
}

func (naptanDoc *NaPTAN) Validate() error {
	if naptanDoc.CreationDateTime == "" {
		return errors.New("CreationDateTime must be set")
	}
	if naptanDoc.ModificationDateTime == "" {
		return errors.New("ModificationDateTime must be set")
	}
	if naptanDoc.SchemaVersion != "2.4" {
		return errors.New(fmt.Sprintf("SchemaVersion must be 2.4 but is %s", naptanDoc.SchemaVersion))
	}

	return nil
}

func (naptanDoc *NaPTAN) Import(dataset datasets.DataSet, datasource *ctdf.DataSource) error {
	if !dataset.SupportedObjects.Stops || !dataset.SupportedObjects.StopGroups {
		return errors.New("This format requires stops & stopgroups to be enabled")
	}

	stopsCollection := database.GetCollection("stops")
	stopGroupsCollection := database.GetCollection("stop_groups")

	// StopAreas
	log.Info().Msg("Converting & Importing CTDF StopGroups into Mongo")
	var stopGroupsOperationInsert uint64

	maxBatchSize := int(math.Ceil(float64(len(naptanDoc.StopAreas)) / float64(runtime.NumCPU())))
	numBatches := int(math.Ceil(float64(len(naptanDoc.StopAreas)) / float64(maxBatchSize)))

	processingGroup := sync.WaitGroup{}
	processingGroup.Add(numBatches)

	stationStopGroups := map[string]bool{}
	stationStopGroupsMutex := sync.Mutex{}

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(naptanDoc.StopAreas) {
			upper = len(naptanDoc.StopAreas)
		}

		batchSlice := naptanDoc.StopAreas[lower:upper]

		go func(stopAreas []*StopArea) {
			var stopGroupOperations []mongo.WriteModel
			var localOperationInsert uint64

			for _, naptanStopArea := range stopAreas {
				ctdfStopGroup := naptanStopArea.ToCTDF()
				ctdfStopGroup.DataSource = datasource

				// Mark stops that are directly part of a station, they are handled specially
				if ctdfStopGroup.Type == "station" || ctdfStopGroup.Type == "port" {
					stationStopGroupsMutex.Lock()
					stationStopGroups[ctdfStopGroup.PrimaryIdentifier] = true
					stationStopGroupsMutex.Unlock()
				}

				transforms.Transform(ctdfStopGroup, 3)

				bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfStopGroup})
				updateModel := mongo.NewUpdateOneModel()
				updateModel.SetFilter(bson.M{"primaryidentifier": ctdfStopGroup.PrimaryIdentifier})
				updateModel.SetUpdate(bsonRep)
				updateModel.SetUpsert(true)

				stopGroupOperations = append(stopGroupOperations, updateModel)
				localOperationInsert += 1
			}

			atomic.AddUint64(&stopGroupsOperationInsert, localOperationInsert)

			if len(stopGroupOperations) > 0 {
				_, err := stopGroupsCollection.BulkWrite(context.Background(), stopGroupOperations, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Err(err).Msg("Failed to bulk write StopGroups")
				}
			}

			processingGroup.Done()
		}(batchSlice)
	}

	processingGroup.Wait()

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", stopGroupsOperationInsert)

	// StopPoints
	log.Info().Msg("Converting & Importing CTDF Stops into Mongo")
	var stopOperationInsert uint64

	maxBatchSize = int(math.Ceil(float64(len(naptanDoc.StopPoints)) / float64(runtime.NumCPU()*10)))
	numBatches = int(math.Ceil(float64(len(naptanDoc.StopPoints)) / float64(maxBatchSize)))

	stationStopGroupContents := map[string][]*StopPoint{}
	stationStopGroupContentsnMutex := sync.Mutex{}
	var stationStops []*StopPoint
	stationStopsMutex := sync.Mutex{}

	processingGroup = sync.WaitGroup{}
	processingGroup.Add(numBatches)

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(naptanDoc.StopPoints) {
			upper = len(naptanDoc.StopPoints)
		}

		batchSlice := naptanDoc.StopPoints[lower:upper]

		go func(stopPoints []*StopPoint) {
			var stopOperations []mongo.WriteModel
			var localOperationInsert uint64

			for _, naptanStopPoint := range stopPoints {
				ctdfStop := naptanStopPoint.ToCTDF()
				ctdfStop.DataSource = datasource

				for _, association := range ctdfStop.Associations {
					if stationStopGroups[association.AssociatedIdentifier] {
						stationStopGroupContentsnMutex.Lock()
						stationStopGroupContents[association.AssociatedIdentifier] = append(stationStopGroupContents[association.AssociatedIdentifier], naptanStopPoint)
						stationStopGroupContentsnMutex.Unlock()
					}
				}

				// Add to list of stations for processing later and then skip it
				if util.ContainsString([]string{
					"MET", "RLY", "FER",
				}, naptanStopPoint.StopClassification.StopType) {
					stationStopsMutex.Lock()
					stationStops = append(stationStops, naptanStopPoint)
					stationStopsMutex.Unlock()

					continue
				}

				// Also skip any station entrances/platforms
				if util.ContainsString([]string{
					"PLT", "RPL", "FBT", "TMU", "RSE", "FTD",
				}, naptanStopPoint.StopClassification.StopType) {
					continue
				}

				transforms.Transform(ctdfStop, 3)

				ctdfStop.DataSource = datasource

				bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfStop})
				updateModel := mongo.NewUpdateOneModel()
				updateModel.SetFilter(bson.M{"primaryidentifier": ctdfStop.PrimaryIdentifier})
				updateModel.SetUpdate(bsonRep)
				updateModel.SetUpsert(true)

				stopOperations = append(stopOperations, updateModel)
				localOperationInsert += 1
			}

			atomic.AddUint64(&stopOperationInsert, localOperationInsert)

			if len(stopOperations) > 0 {
				_, err := stopsCollection.BulkWrite(context.Background(), stopOperations, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Err(err).Msg("Failed to bulk write Stops")
				}
			}

			processingGroup.Done()
		}(batchSlice)
	}

	processingGroup.Wait()

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", stopOperationInsert)

	// Specially handle generating new station stops
	log.Info().Msg("Converting & Importing CTDF station Stops into Mongo")
	var stationStopOperations []mongo.WriteModel
	var stationStopOperationInsert int

	for _, stationNaptanStop := range stationStops {
		stationStop := stationNaptanStop.ToCTDF()
		stationStop.DataSource = datasource

		var stopGroupStops []*StopPoint
		for _, area := range stationNaptanStop.StopAreas {
			stopGroupStops = append(stopGroupStops, stationStopGroupContents[fmt.Sprintf("GB:STOPGRP:%s", area.StopAreaCode)]...)
		}

		// Find all platforms & entrances and add them to the stops
		for _, stopPoint := range stopGroupStops {
			// PLT - Metro/tram
			// RPL - Rail
			// FBT - Ferry
			if stopPoint.StopClassification.StopType == "PLT" || stopPoint.StopClassification.StopType == "RPL" || stopPoint.StopClassification.StopType == "FBT" {
				stop := stopPoint.ToCTDF()
				stationStop.Platforms = append(stationStop.Platforms, &ctdf.StopPlatform{
					PrimaryIdentifier: stop.PrimaryIdentifier,

					PrimaryName: stop.PrimaryName,

					Location: stop.Location,
				})
				stationStop.OtherIdentifiers = append(stationStop.OtherIdentifiers, stop.PrimaryIdentifier)
			} else {
				// TMU - Metro/tram
				// RSE - Rail
				// FTD - Ferry
				// if stopPoint.StopClassification.StopType == "TMU" || stopPoint.StopClassification.StopType == "RSE" || stopPoint.StopClassification.StopType == "FTD" {
				// 	stop := stopPoint.ToCTDF()
				// 	stationStop.Entrances = append(stationStop.Entrances, &ctdf.StopEntrance{
				// 		PrimaryIdentifier: stop.PrimaryIdentifier,

				// 		PrimaryName: stop.PrimaryName,

				// 		Location: stop.Location,
				// 	})
				// 	stationStop.OtherIdentifiers = append(stationStop.OtherIdentifiers, stop.PrimaryIdentifier)
				// }
			}
		}

		transforms.Transform(stationStop, 2)

		bsonRep, _ := bson.Marshal(bson.M{"$set": stationStop})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(bson.M{"primaryidentifier": stationStop.PrimaryIdentifier})
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		stationStopOperations = append(stationStopOperations, updateModel)
		stationStopOperationInsert += 1
	}

	if len(stationStopOperations) > 0 {
		_, err := stopsCollection.BulkWrite(context.Background(), stationStopOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write station Stops")
		}
	}
	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", stationStopOperationInsert)

	log.Info().Msgf("Successfully imported into MongoDB")

	return nil
}
