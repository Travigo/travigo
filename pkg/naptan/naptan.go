package naptan

import (
	"context"
	"errors"
	"math"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

const DateTimeFormat string = "2006-01-02T15:04:05"

type NaPTAN struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	SchemaVersion string `xml:",attr"`

	StopPoints []*StopPoint
	StopAreas  []*StopArea
}

func (n *NaPTAN) Validate() error {
	if n.CreationDateTime == "" {
		return errors.New("CreationDateTime must be set")
	}
	if n.ModificationDateTime == "" {
		return errors.New("ModificationDateTime must be set")
	}
	if n.SchemaVersion != "2.4" {
		return errors.New("SchemaVersion must be 2.4")
	}

	return nil
}

func (naptanDoc *NaPTAN) ImportIntoMongoAsCTDF() {
	stopsCollection := database.GetCollection("stops")
	stopGroupsCollection := database.GetCollection("stop_groups")

	// TODO: Doesnt really make sense for the NaPTAN package to be managing CTDF tables and indexes
	stopsIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "location", Value: bsonx.String("2dsphere")}},
		},
	}

	opts := options.CreateIndexes()
	_, err := stopsCollection.Indexes().CreateMany(context.Background(), stopsIndex, opts)
	if err != nil {
		panic(err)
	}

	stopGroupsIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "identifier", Value: bsonx.Int32(1)}},
		},
	}

	opts = options.CreateIndexes()
	_, err = stopGroupsCollection.Indexes().CreateMany(context.Background(), stopGroupsIndex, opts)
	if err != nil {
		panic(err)
	}

	// StopPoints
	log.Info().Msg("Converting & Importing CTDF Stops into Mongo")
	var stopOperationInsert uint64
	var stopOperationUpdate uint64

	maxBatchSize := int(len(naptanDoc.StopPoints) / runtime.NumCPU())
	numBatches := int(math.Ceil(float64(len(naptanDoc.StopPoints)) / float64(maxBatchSize)))

	processingGroup := sync.WaitGroup{}
	processingGroup.Add(numBatches)

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(naptanDoc.StopPoints) {
			upper = len(naptanDoc.StopPoints)
		}

		batchSlice := naptanDoc.StopPoints[lower:upper]

		go func(stopPoints []*StopPoint) {
			stopOperations := []mongo.WriteModel{}

			for _, naptanStopPoint := range stopPoints {
				ctdfStop := naptanStopPoint.ToCTDF()
				bsonRep, _ := bson.Marshal(ctdfStop)

				var existingCtdfStop *ctdf.Stop
				stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": ctdfStop.PrimaryIdentifier}).Decode(&existingCtdfStop)

				if existingCtdfStop == nil {
					insertModel := mongo.NewInsertOneModel()
					insertModel.SetDocument(bsonRep)

					stopOperations = append(stopOperations, insertModel)
					atomic.AddUint64(&stopOperationInsert, 1)
				} else if existingCtdfStop.ModificationDateTime != ctdfStop.ModificationDateTime {
					updateModel := mongo.NewReplaceOneModel()
					updateModel.SetFilter(bson.M{"primaryidentifier": ctdfStop.PrimaryIdentifier})
					updateModel.SetReplacement(bsonRep)

					stopOperations = append(stopOperations, updateModel)
					atomic.AddUint64(&stopOperationUpdate, 1)
				}
			}

			if len(stopOperations) > 0 {
				_, err = stopsCollection.BulkWrite(context.TODO(), stopOperations, &options.BulkWriteOptions{})
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

	// StopAreas
	log.Info().Msg("Converting & Importing CTDF StopGroups into Mongo")
	var stopGroupsOperationInsert uint64
	var stopGroupsOperationUpdate uint64

	maxBatchSize = int(len(naptanDoc.StopAreas) / runtime.NumCPU())
	numBatches = int(math.Ceil(float64(len(naptanDoc.StopAreas)) / float64(maxBatchSize)))

	processingGroup = sync.WaitGroup{}
	processingGroup.Add(numBatches)

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(naptanDoc.StopAreas) {
			upper = len(naptanDoc.StopAreas)
		}

		batchSlice := naptanDoc.StopAreas[lower:upper]

		go func(stopAreas []*StopArea) {
			stopGroupOperations := []mongo.WriteModel{}

			for _, naptanStopArea := range stopAreas {
				ctdfStopGroup := naptanStopArea.ToCTDF()

				var existingStopGroup *ctdf.StopGroup
				stopGroupsCollection.FindOne(context.Background(), bson.M{"identifier": ctdfStopGroup.Identifier}).Decode(&existingStopGroup)

				if existingStopGroup == nil {
					insertModel := mongo.NewInsertOneModel()

					bsonRep, _ := bson.Marshal(ctdfStopGroup)
					insertModel.SetDocument(bsonRep)

					stopGroupOperations = append(stopGroupOperations, insertModel)

					atomic.AddUint64(&stopGroupsOperationInsert, 1)
				} else if existingStopGroup.ModificationDateTime != ctdfStopGroup.ModificationDateTime {
					updateModel := mongo.NewUpdateOneModel()

					updateModel.SetFilter(bson.M{"primaryidentifier": ctdfStopGroup.Identifier})

					bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfStopGroup})
					updateModel.SetUpdate(bsonRep)

					stopGroupOperations = append(stopGroupOperations, updateModel)

					atomic.AddUint64(&stopGroupsOperationUpdate, 1)
				}
			}

			if len(stopGroupOperations) > 0 {
				_, err = stopGroupsCollection.BulkWrite(context.TODO(), stopGroupOperations, &options.BulkWriteOptions{})
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
}
