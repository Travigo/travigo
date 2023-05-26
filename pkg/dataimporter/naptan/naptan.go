package naptan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"

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

func (naptanDoc *NaPTAN) ImportIntoMongoAsCTDF(datasource *ctdf.DataSource) {
	datasource.OriginalFormat = "naptan"

	stopsCollection := database.GetCollection("stops")
	stopGroupsCollection := database.GetCollection("stop_groups")

	// StopAreas
	log.Info().Msg("Converting & Importing CTDF StopGroups into Mongo")
	var stopGroupsOperationInsert uint64
	var stopGroupsOperationUpdate uint64

	maxBatchSize := int(math.Ceil(float64(len(naptanDoc.StopAreas)) / float64(runtime.NumCPU())))
	numBatches := int(math.Ceil(float64(len(naptanDoc.StopAreas)) / float64(maxBatchSize)))

	processingGroup := sync.WaitGroup{}
	processingGroup.Add(numBatches)

	stationStopGroups := map[string]bool{}

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
			var localOperationUpdate uint64

			for _, naptanStopArea := range stopAreas {
				ctdfStopGroup := naptanStopArea.ToCTDF()
				ctdfStopGroup.DataSource = datasource

				// Mark stops that are directly part of a station, they are handled specially
				if ctdfStopGroup.Type == "station" {
					stationStopGroups[ctdfStopGroup.PrimaryIdentifier] = true
				}

				var existingStopGroup *ctdf.StopGroup
				stopGroupsCollection.FindOne(context.Background(), bson.M{"identifier": ctdfStopGroup.PrimaryIdentifier}).Decode(&existingStopGroup)

				if existingStopGroup == nil {
					insertModel := mongo.NewInsertOneModel()

					bsonRep, _ := bson.Marshal(ctdfStopGroup)
					insertModel.SetDocument(bsonRep)

					stopGroupOperations = append(stopGroupOperations, insertModel)
					localOperationInsert += 1
				} else if existingStopGroup.ModificationDateTime.Before(ctdfStopGroup.ModificationDateTime) || existingStopGroup.ModificationDateTime.Year() == 0 {
					updateModel := mongo.NewUpdateOneModel()

					updateModel.SetFilter(bson.M{"primaryidentifier": ctdfStopGroup.PrimaryIdentifier})

					bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfStopGroup})
					updateModel.SetUpdate(bsonRep)

					stopGroupOperations = append(stopGroupOperations, updateModel)
					localOperationUpdate += 1
				}
			}

			atomic.AddUint64(&stopGroupsOperationInsert, localOperationInsert)
			atomic.AddUint64(&stopGroupsOperationUpdate, localOperationUpdate)

			if len(stopGroupOperations) > 0 {
				_, err := stopGroupsCollection.BulkWrite(context.TODO(), stopGroupOperations, &options.BulkWriteOptions{})
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
	log.Info().Msgf(" - %d updates", stopGroupsOperationUpdate)

	// StopPoints
	log.Info().Msg("Converting & Importing CTDF Stops into Mongo")
	var stopOperationInsert uint64
	var stopOperationUpdate uint64

	maxBatchSize = int(math.Ceil(float64(len(naptanDoc.StopPoints)) / float64(runtime.NumCPU()*10)))
	numBatches = int(math.Ceil(float64(len(naptanDoc.StopPoints)) / float64(maxBatchSize)))

	stationStopGroupContents := map[string][]*StopPoint{}
	stationStopGroupContentsnMutex := sync.Mutex{}

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
			var localOperationUpdate uint64

		CREATESTOPLOOP:
			for _, naptanStopPoint := range stopPoints {
				ctdfStop := naptanStopPoint.ToCTDF()

				for _, association := range ctdfStop.Associations {
					if stationStopGroups[association.AssociatedIdentifier] {
						stationStopGroupContentsnMutex.Lock()
						stationStopGroupContents[association.AssociatedIdentifier] = append(stationStopGroupContents[association.AssociatedIdentifier], naptanStopPoint)
						stationStopGroupContentsnMutex.Unlock()

						continue CREATESTOPLOOP
					}
				}

				ctdfStop.DataSource = datasource
				bsonRep, _ := bson.Marshal(ctdfStop)

				var existingCtdfStop *ctdf.Stop
				stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": ctdfStop.PrimaryIdentifier}).Decode(&existingCtdfStop)

				if existingCtdfStop == nil {
					insertModel := mongo.NewInsertOneModel()
					insertModel.SetDocument(bsonRep)

					stopOperations = append(stopOperations, insertModel)
					localOperationInsert += 1
				} else if existingCtdfStop.ModificationDateTime.Before(ctdfStop.ModificationDateTime) || existingCtdfStop.ModificationDateTime.Year() == 0 {
					updateModel := mongo.NewReplaceOneModel()
					updateModel.SetFilter(bson.M{"primaryidentifier": ctdfStop.PrimaryIdentifier})
					updateModel.SetReplacement(bsonRep)

					stopOperations = append(stopOperations, updateModel)
					localOperationUpdate += 1
				}
			}

			atomic.AddUint64(&stopOperationInsert, localOperationInsert)
			atomic.AddUint64(&stopOperationUpdate, localOperationUpdate)

			if len(stopOperations) > 0 {
				_, err := stopsCollection.BulkWrite(context.TODO(), stopOperations, &options.BulkWriteOptions{})
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
	log.Info().Msgf(" - %d updates", stopOperationUpdate)

	// Specially handle generating new station stops
	log.Info().Msg("Converting & Importing CTDF station Stops into Mongo")
	var stationStopOperations []mongo.WriteModel
	var stationStopOperationInsert int
	var stationStopOperationUpdate int

	for key, stopPoints := range stationStopGroupContents {
		var stationStop *ctdf.Stop

		// Find the main station descriptor and convert that first
		for _, stopPoint := range stopPoints {
			if stopPoint.StopClassification.StopType == "MET" || stopPoint.StopClassification.StopType == "RLY" {
				stationStop = stopPoint.ToCTDF()
			}
		}

		if stationStop == nil {
			log.Error().Str("key", key).Msg("Unhandled station stop group")
			continue
		}

		// Find all platforms & entrances and add them to the stops
		for _, stopPoint := range stopPoints {
			if stopPoint.StopClassification.StopType == "PLT" {
				stop := stopPoint.ToCTDF()
				stationStop.Platforms = append(stationStop.Platforms, &ctdf.StopPlatform{
					PrimaryIdentifier: stop.PrimaryIdentifier,
					OtherIdentifiers:  stop.OtherIdentifiers,

					PrimaryName: stop.PrimaryName,
					OtherNames:  stop.OtherNames,

					Location: stop.Location,
				})
			} else {
				if stopPoint.StopClassification.StopType == "TMU" || stopPoint.StopClassification.StopType == "RSE" {
					stop := stopPoint.ToCTDF()
					stationStop.Entrances = append(stationStop.Entrances, &ctdf.StopEntrance{
						PrimaryIdentifier: stop.PrimaryIdentifier,
						OtherIdentifiers:  stop.OtherIdentifiers,

						PrimaryName: stop.PrimaryName,
						OtherNames:  stop.OtherNames,

						Location: stop.Location,
					})
				}
			}
		}

		bsonRep, _ := bson.Marshal(stationStop)

		var existingCtdfStop *ctdf.Stop
		stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": stationStop.PrimaryIdentifier}).Decode(&existingCtdfStop)

		if existingCtdfStop == nil {
			insertModel := mongo.NewInsertOneModel()
			insertModel.SetDocument(bsonRep)

			stationStopOperations = append(stationStopOperations, insertModel)
			stationStopOperationInsert += 1
		} else if existingCtdfStop.ModificationDateTime.Before(stationStop.ModificationDateTime) || existingCtdfStop.ModificationDateTime.Year() == 0 {
			updateModel := mongo.NewReplaceOneModel()
			updateModel.SetFilter(bson.M{"primaryidentifier": stationStop.PrimaryIdentifier})
			updateModel.SetReplacement(bsonRep)

			stationStopOperations = append(stationStopOperations, updateModel)
			stationStopOperationUpdate += 1
		}
	}
	if len(stationStopOperations) > 0 {
		_, err := stopsCollection.BulkWrite(context.TODO(), stationStopOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write station Stops")
		}
	}
	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", stationStopOperationInsert)
	log.Info().Msgf(" - %d updates", stationStopOperationUpdate)

	log.Info().Msgf("Successfully imported into MongoDB")
}
