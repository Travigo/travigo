package naptan

import (
	"context"
	"errors"
	"fmt"
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

const (
	DateTimeFormat            string = "2006-01-02T15:04:05"
	defaultBulkWriteOrdered          = false
	defaultStopGroupBatchSize        = 5000
	defaultStopBatchSize             = 5000
)

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

func (naptanDoc *NaPTAN) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) error {
	if !dataset.SupportedObjects.Stops || !dataset.SupportedObjects.StopGroups {
		return errors.New("This format requires stops & stopgroups to be enabled")
	}

	stopsCollection := database.GetCollection("stops_raw")
	stopGroupsCollection := database.GetCollection("stop_groups")

	// StopAreas
	log.Info().Msg("Converting & Importing CTDF StopGroups into Mongo")
	var stopGroupsOperationInsert uint64

	stationStopGroups := map[string]bool{}
	stationStopGroupsMutex := sync.Mutex{}

	if len(naptanDoc.StopAreas) > 0 {
		batchSize := defaultStopGroupBatchSize
		numBatches := (len(naptanDoc.StopAreas) + batchSize - 1) / batchSize
		processingGroup := sync.WaitGroup{}
		processingGroup.Add(numBatches)

		ordered := defaultBulkWriteOrdered
		bulkOpts := &options.BulkWriteOptions{Ordered: &ordered}

		for i := 0; i < numBatches; i++ {
			lower := batchSize * i
			upper := batchSize * (i + 1)
			if upper > len(naptanDoc.StopAreas) {
				upper = len(naptanDoc.StopAreas)
			}
			batch := naptanDoc.StopAreas[lower:upper]

			go func(stopAreas []*StopArea) {
				defer processingGroup.Done()
				stopGroupOperations := make([]mongo.WriteModel, 0, len(stopAreas))
				var localOperationInsert uint64

				for _, naptanStopArea := range stopAreas {
					ctdfStopGroup := naptanStopArea.ToCTDF()
					ctdfStopGroup.DataSource = datasource

					if ctdfStopGroup.Type == "station" || ctdfStopGroup.Type == "port" {
						stationStopGroupsMutex.Lock()
						stationStopGroups[ctdfStopGroup.PrimaryIdentifier] = true
						stationStopGroupsMutex.Unlock()
					}

					transforms.Transform(ctdfStopGroup, 3)

					updateModel := mongo.NewUpdateOneModel().
						SetFilter(bson.M{"primaryidentifier": ctdfStopGroup.PrimaryIdentifier}).
						SetUpdate(bson.M{"$set": ctdfStopGroup}).
						SetUpsert(true)

					stopGroupOperations = append(stopGroupOperations, updateModel)
					localOperationInsert++
				}

				atomic.AddUint64(&stopGroupsOperationInsert, localOperationInsert)
				if len(stopGroupOperations) > 0 {
					if _, err := stopGroupsCollection.BulkWrite(context.Background(), stopGroupOperations, bulkOpts); err != nil {
						log.Error().Err(err).Msg("Failed to bulk write StopGroups")
					}
				}
			}(batch)
		}

		processingGroup.Wait()
	}

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", stopGroupsOperationInsert)

	// StopPoints
	log.Info().Msg("Converting & Importing CTDF Stops into Mongo")
	var stopOperationInsert uint64

	stationStopGroupContents := map[string][]*StopPoint{}
	stationStopGroupContentsMutex := sync.RWMutex{}
	var stationStops []*StopPoint
	stationStopsMutex := sync.Mutex{}

	if len(naptanDoc.StopPoints) > 0 {
		batchSize := defaultStopBatchSize
		numBatches := (len(naptanDoc.StopPoints) + batchSize - 1) / batchSize
		processingGroup := sync.WaitGroup{}
		processingGroup.Add(numBatches)

		ordered := defaultBulkWriteOrdered
		bulkOpts := &options.BulkWriteOptions{Ordered: &ordered}

		for i := 0; i < numBatches; i++ {
			lower := batchSize * i
			upper := batchSize * (i + 1)
			if upper > len(naptanDoc.StopPoints) {
				upper = len(naptanDoc.StopPoints)
			}
			batch := naptanDoc.StopPoints[lower:upper]

			go func(stopPoints []*StopPoint) {
				defer processingGroup.Done()
				stopOperations := make([]mongo.WriteModel, 0, len(stopPoints))
				var localOperationInsert uint64

				for _, naptanStopPoint := range stopPoints {
					ctdfStop := naptanStopPoint.ToCTDF()
					ctdfStop.DataSource = datasource

					for _, association := range ctdfStop.Associations {
						if stationStopGroups[association.AssociatedIdentifier] {
							stationStopGroupContentsMutex.Lock()
							stationStopGroupContents[association.AssociatedIdentifier] = append(stationStopGroupContents[association.AssociatedIdentifier], naptanStopPoint)
							stationStopGroupContentsMutex.Unlock()
						}
					}

					if util.ContainsString([]string{"MET", "RLY", "FER"}, naptanStopPoint.StopClassification.StopType) {
						stationStopsMutex.Lock()
						stationStops = append(stationStops, naptanStopPoint)
						stationStopsMutex.Unlock()
						continue
					}

					if util.ContainsString([]string{"PLT", "RPL", "FBT", "TMU", "RSE", "FTD"}, naptanStopPoint.StopClassification.StopType) {
						continue
					}

					transforms.Transform(ctdfStop, 3)

					updateModel := mongo.NewUpdateOneModel().
						SetFilter(bson.M{"primaryidentifier": ctdfStop.PrimaryIdentifier}).
						SetUpdate(bson.M{"$set": ctdfStop}).
						SetUpsert(true)

					stopOperations = append(stopOperations, updateModel)
					localOperationInsert++
				}

				atomic.AddUint64(&stopOperationInsert, localOperationInsert)
				if len(stopOperations) > 0 {
					if _, err := stopsCollection.BulkWrite(context.Background(), stopOperations, bulkOpts); err != nil {
						log.Error().Err(err).Msg("Failed to bulk write Stops")
					}
				}
			}(batch)
		}

		processingGroup.Wait()
	}

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", stopOperationInsert)

	// Specially handle generating new station stops
	log.Info().Msg("Converting & Importing CTDF station Stops into Mongo")
	stationStopOperations := make([]mongo.WriteModel, 0, len(stationStops))
	stationStopOperationInsert := 0

	for _, stationNaptanStop := range stationStops {
		stationStop := stationNaptanStop.ToCTDF()
		stationStop.DataSource = datasource

		var stopGroupStops []*StopPoint
		for _, area := range stationNaptanStop.StopAreas {
			key := fmt.Sprintf("gb-stopgroup-%s", area.StopAreaCode)
			stationStopGroupContentsMutex.RLock()
			stopGroupStops = append(stopGroupStops, stationStopGroupContents[key]...)
			stationStopGroupContentsMutex.RUnlock()
		}

		for _, stopPoint := range stopGroupStops {
			if stopPoint.StopClassification.StopType == "PLT" || stopPoint.StopClassification.StopType == "RPL" || stopPoint.StopClassification.StopType == "FBT" {
				stop := stopPoint.ToCTDF()
				stationStop.Platforms = append(stationStop.Platforms, &ctdf.StopPlatform{PrimaryIdentifier: stop.PrimaryIdentifier, PrimaryName: stop.PrimaryName, Location: stop.Location})
				stationStop.OtherIdentifiers = append(stationStop.OtherIdentifiers, stop.PrimaryIdentifier)
			}
		}

		transforms.Transform(stationStop, 2)

		updateModel := mongo.NewUpdateOneModel().
			SetFilter(bson.M{"primaryidentifier": stationStop.PrimaryIdentifier}).
			SetUpdate(bson.M{"$set": stationStop}).
			SetUpsert(true)

		stationStopOperations = append(stationStopOperations, updateModel)
		stationStopOperationInsert++
	}

	if len(stationStopOperations) > 0 {
		ordered2 := defaultBulkWriteOrdered
		bulkOpts2 := &options.BulkWriteOptions{Ordered: &ordered2}
		if _, err := stopsCollection.BulkWrite(context.Background(), stationStopOperations, bulkOpts2); err != nil {
			log.Error().Err(err).Msg("Failed to bulk write station Stops")
		}
	}

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", stationStopOperationInsert)
	log.Info().Msgf("Successfully imported into MongoDB")

	return nil
}
