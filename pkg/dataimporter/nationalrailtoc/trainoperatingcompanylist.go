package nationalrailtoc

import (
	"context"
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

	StopNameOverrides := generateRailStopNameOverrides()

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

			StopNameOverrides: StopNameOverrides,
		})
	}

	return operators, services
}

func (t *TrainOperatingCompanyList) ImportIntoMongoAsCTDF(datasource *ctdf.DataSource) {
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
	var operatorOperationUpdate uint64

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
			var localOperationUpdate uint64

			for _, operator := range operatorsBatch {
				var existingCtdfOperator *ctdf.Operator
				operatorsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": operator.PrimaryIdentifier}).Decode(&existingCtdfOperator)

				if existingCtdfOperator == nil {
					operator.CreationDateTime = time.Now()
					operator.ModificationDateTime = time.Now()
					operator.DataSource = datasource

					insertModel := mongo.NewInsertOneModel()

					bsonRep, _ := bson.Marshal(operator)
					insertModel.SetDocument(bsonRep)

					operatorOperations = append(operatorOperations, insertModel)
					localOperationInsert += 1
				} else if existingCtdfOperator.UniqueHash() != operator.UniqueHash() {
					operator.CreationDateTime = existingCtdfOperator.CreationDateTime
					operator.ModificationDateTime = time.Now()
					operator.DataSource = datasource

					updateModel := mongo.NewUpdateOneModel()
					updateModel.SetFilter(bson.M{"primaryidentifier": operator.PrimaryIdentifier})

					bsonRep, _ := bson.Marshal(bson.M{"$set": operator})
					updateModel.SetUpdate(bsonRep)

					operatorOperations = append(operatorOperations, updateModel)
					localOperationUpdate += 1
				}
			}

			atomic.AddUint64(&operatorOperationInsert, localOperationInsert)
			atomic.AddUint64(&operatorOperationUpdate, localOperationUpdate)

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
	log.Info().Msgf(" - %d updates", operatorOperationUpdate)

	// Import services
	log.Info().Msg("Importing CTDF Services into Mongo")
	var servicesOperationInsert uint64
	var servicesOperationUpdate uint64

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
			var localServicesUpdate uint64

			for _, service := range servicesBatch {
				var existingCtdfService *ctdf.Service
				servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": service.PrimaryIdentifier}).Decode(&existingCtdfService)

				if existingCtdfService == nil {
					service.CreationDateTime = time.Now()
					service.ModificationDateTime = time.Now()
					service.DataSource = datasource

					insertModel := mongo.NewInsertOneModel()

					bsonRep, _ := bson.Marshal(service)
					insertModel.SetDocument(bsonRep)

					servicesOperations = append(servicesOperations, insertModel)
					localServicesInsert += 1
				} else if existingCtdfService.ModificationDateTime != service.ModificationDateTime {
					service.CreationDateTime = existingCtdfService.CreationDateTime
					service.ModificationDateTime = time.Now()
					service.DataSource = datasource

					updateModel := mongo.NewUpdateOneModel()
					updateModel.SetFilter(bson.M{"primaryidentifier": service.PrimaryIdentifier})

					bsonRep, _ := bson.Marshal(bson.M{"$set": service})
					updateModel.SetUpdate(bsonRep)

					servicesOperations = append(servicesOperations, updateModel)
					localServicesUpdate += 1
				}
			}

			atomic.AddUint64(&servicesOperationInsert, localServicesInsert)
			atomic.AddUint64(&servicesOperationUpdate, localServicesUpdate)

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
	log.Info().Msgf(" - %d updates", servicesOperationUpdate)
}

func generateRailStopNameOverrides() map[string]string {
	stopNameOverrides := map[string]string{}

	stopsCollection := database.GetCollection("stops")
	var stops []ctdf.Stop
	cursor, _ := stopsCollection.Find(context.Background(), bson.M{"otheridentifiers.Tiploc": bson.M{"$exists": true}})
	cursor.All(context.Background(), &stops)

	for _, stop := range stops {
		stopNameOverrides[stop.PrimaryIdentifier] = strings.Replace(stop.PrimaryName, " Rail Station", "", 1)
	}

	return stopNameOverrides
}
