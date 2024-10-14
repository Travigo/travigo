package nationalrailtoc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type TrainOperatingCompanyList struct {
	Companies []TrainOperatingCompany `xml:"TrainOperatingCompany"`
}

func (t *TrainOperatingCompanyList) convertToCTDF() ([]*ctdf.Operator, []*ctdf.Service) {
	var operators []*ctdf.Operator
	var services []*ctdf.Service

	stopNameOverrides := generateRailStopNameOverrides()

	now := time.Now()

	for _, toc := range t.Companies {
		operatorRef := fmt.Sprintf(ctdf.OperatorTOCFormat, toc.AtocCode)

		address := ""

		address += strings.Join(toc.CustomerService.PostalAddress.Line, ", ")
		address += fmt.Sprintf(", %s", toc.CustomerService.PostalAddress.PostCode)

		operators = append(operators, &ctdf.Operator{
			PrimaryIdentifier: operatorRef,
			OtherIdentifiers:  []string{operatorRef},

			CreationDateTime:     now,
			ModificationDateTime: now,

			PrimaryName: toc.Name,
			OtherNames:  []string{toc.LegalName},

			TransportType: ctdf.TransportTypeRail,

			Website:     toc.CompanyWebsite,
			Email:       toc.CustomerService.EmailAddress,
			Address:     address,
			PhoneNumber: toc.CustomerService.Telephone,
			SocialMedia: map[string]string{},
		})

		services = append(services, &ctdf.Service{
			PrimaryIdentifier:    operatorRef,
			CreationDateTime:     now,
			ModificationDateTime: now,
			ServiceName:          toc.Name,
			OperatorRef:          operatorRef,
			TransportType:        ctdf.TransportTypeRail,

			BrandIcon:        "/icons/national-rail.svg",
			BrandColour:      "#ffffff",
			BrandDisplayMode: "short",

			StopNameOverrides: stopNameOverrides,
		})
	}

	return operators, services
}

func (t *TrainOperatingCompanyList) Import(dataset datasets.DataSet, datasource *ctdf.DataSource) error {
	if !dataset.SupportedObjects.Operators || !dataset.SupportedObjects.Services {
		return errors.New("This format requires operators & services to be enabled")
	}

	operators, services := t.convertToCTDF()

	log.Info().Msg("Converting to CTDF")
	log.Info().Msgf(" - %d Operators", len(operators))
	log.Info().Msgf(" - %d Services", len(services))

	// Tables
	operatorsCollection := database.GetCollection("operators")
	servicesCollection := database.GetCollection("services")

	// Import operators
	log.Info().Msg("Importing CTDF Operators into Mongo")
	var operatorOperationInsert uint64

	maxBatchSize := int(math.Ceil(float64(len(operators)) / float64(runtime.NumCPU())))
	numBatches := int(math.Ceil(float64(len(operators)) / float64(maxBatchSize)))

	processingGroup := sync.WaitGroup{}
	processingGroup.Add(numBatches)

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(operators) {
			upper = len(operators)
		}

		batchSlice := operators[lower:upper]

		go func(operatorsBatch []*ctdf.Operator) {
			var operatorOperations []mongo.WriteModel
			var localOperationInsert uint64

			for _, operator := range operatorsBatch {
				operator.CreationDateTime = time.Now()
				operator.ModificationDateTime = time.Now()
				operator.DataSource = datasource

				bsonRep, _ := bson.Marshal(bson.M{"$set": operator})
				updateModel := mongo.NewUpdateOneModel()
				updateModel.SetFilter(bson.M{"primaryidentifier": operator.PrimaryIdentifier})
				updateModel.SetUpdate(bsonRep)
				updateModel.SetUpsert(true)

				operatorOperations = append(operatorOperations, updateModel)
				localOperationInsert += 1
			}

			atomic.AddUint64(&operatorOperationInsert, localOperationInsert)

			if len(operatorOperations) > 0 {
				_, err := operatorsCollection.BulkWrite(context.Background(), operatorOperations, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Err(err).Msg("Failed to bulk write Operators")
				}
			}

			processingGroup.Done()
		}(batchSlice)
	}

	processingGroup.Wait()

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", operatorOperationInsert)

	// Import services
	log.Info().Msg("Importing CTDF Services into Mongo")
	var servicesOperationInsert uint64

	maxBatchSize = int(math.Ceil(float64(len(services)) / float64(runtime.NumCPU())))
	numBatches = int(math.Ceil(float64(len(services)) / float64(maxBatchSize)))

	processingGroup = sync.WaitGroup{}
	processingGroup.Add(numBatches)

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(services) {
			upper = len(operators)
		}

		batchSlice := services[lower:upper]

		go func(servicesBatch []*ctdf.Service) {
			var servicesOperations []mongo.WriteModel
			var localServicesInsert uint64

			for _, service := range servicesBatch {
				service.CreationDateTime = time.Now()
				service.ModificationDateTime = time.Now()
				service.DataSource = datasource

				bsonRep, _ := bson.Marshal(bson.M{"$set": service})
				updateModel := mongo.NewUpdateOneModel()
				updateModel.SetFilter(bson.M{"primaryidentifier": service.PrimaryIdentifier})
				updateModel.SetUpdate(bsonRep)
				updateModel.SetUpsert(true)

				servicesOperations = append(servicesOperations, updateModel)
				localServicesInsert += 1

			}

			atomic.AddUint64(&servicesOperationInsert, localServicesInsert)

			if len(servicesOperations) > 0 {
				_, err := servicesCollection.BulkWrite(context.Background(), servicesOperations, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Err(err).Msg("Failed to bulk write Services")
				}
			}

			processingGroup.Done()
		}(batchSlice)
	}

	processingGroup.Wait()

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", servicesOperationInsert)

	return nil
}

func generateRailStopNameOverrides() map[string]string {
	stopNameOverrides := map[string]string{}

	stopsCollection := database.GetCollection("stops")
	var stops []ctdf.Stop
	cursor, _ := stopsCollection.Find(context.Background(), bson.M{"otheridentifiers": bson.M{"$regex": "^GB:TIPLOC:"}})
	cursor.All(context.Background(), &stops)

	for _, stop := range stops {
		stopNameOverrides[stop.PrimaryIdentifier] = strings.Replace(stop.PrimaryName, " Rail Station", "", 1)
	}

	return stopNameOverrides
}
