package travelinenoc

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/util"
	"github.com/britbus/notify/pkg/notify_client"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

type TravelineData struct {
	GenerationDate             string `xml:",attr"`
	NOCLinesRecords            []NOCLinesRecord
	NOCTableRecords            []NOCTableRecord
	OperatorsRecords           []OperatorsRecord
	GroupsRecords              []GroupsRecord
	ManagementDivisionsRecords []ManagementDivisionsRecord
	PublicNameRecords          []PublicNameRecord
}

func extractContactDetails(value string, ctdfOperator *ctdf.Operator) {
	emailRegex, _ := regexp.Compile("^[^@]+@[^@]+.[^@]+$")
	phoneRegex, _ := regexp.Compile("^[\\d ]+$")
	addressRegex, _ := regexp.Compile("^[a-zA-Z\\d ,]+$")

	if emailRegex.MatchString(value) {
		ctdfOperator.Email = value
	} else if phoneRegex.MatchString(value) {
		ctdfOperator.PhoneNumber = value
	} else if addressRegex.MatchString(value) {
		ctdfOperator.Address = value
	}
}

func (t *TravelineData) convertToCTDF() ([]*ctdf.Operator, []*ctdf.OperatorGroup) {
	operators := []*ctdf.Operator{}
	operatorGroups := []*ctdf.OperatorGroup{}

	mgmtDivisionGroupIDs := map[string]string{}
	operatorNOCCodeRef := map[string]*ctdf.Operator{}
	operatorsIDRef := map[string]*ctdf.Operator{}
	publicNameIDRef := map[string]*ctdf.Operator{}

	// GroupsRecords
	for _, groupRecord := range t.GroupsRecords {
		ctdfRecord := &ctdf.OperatorGroup{
			Identifier: fmt.Sprintf(ctdf.OperatorGroupIDFormat, groupRecord.GroupID),
			Name:       groupRecord.GroupName,
		}
		operatorGroups = append(operatorGroups, ctdfRecord)
	}

	// ManagementDivisionsRecord
	for _, mgmtDivisionRecord := range t.ManagementDivisionsRecords {
		// Create a reference between the Management Division ID and the Group ID
		mgmtDivisionGroupIDs[mgmtDivisionRecord.ManagementDivisionID] = mgmtDivisionRecord.GroupID
	}

	// OperatorsRecords
	for _, operatorRecord := range t.OperatorsRecords {
		ctdfRecord := &ctdf.Operator{
			PrimaryName:       operatorRecord.OperatorName,
			PrimaryIdentifier: fmt.Sprintf(ctdf.OperatorNOCIDFormat, operatorRecord.OperatorID),
			OtherIdentifiers:  []string{},
			OtherNames:        []string{},
		}

		ctdfRecord.OtherNames = append(ctdfRecord.OtherNames, operatorRecord.OperatorName)

		if operatorRecord.ManagementDivisionID != "" {
			groupID := mgmtDivisionGroupIDs[operatorRecord.ManagementDivisionID]

			ctdfRecord.OperatorGroupRef = fmt.Sprintf(ctdf.OperatorGroupIDFormat, groupID)
		}

		operators = append(operators, ctdfRecord)
		operatorsIDRef[operatorRecord.OperatorID] = ctdfRecord
	}

	// NOCTableRecords
	for _, nocTableRecord := range t.NOCTableRecords {
		operator := operatorsIDRef[nocTableRecord.OperatorID]

		if operator != nil {
			operator.PrimaryName = nocTableRecord.OperatorPublicName

			operator.OtherIdentifiers = append(operator.OtherIdentifiers, fmt.Sprintf(ctdf.OperatorNOCFormat, nocTableRecord.NOCCode))
			operator.OtherNames = append(operator.OtherNames, nocTableRecord.OperatorPublicName, nocTableRecord.VOSA_PSVLicenseName)

			operatorNOCCodeRef[nocTableRecord.NOCCode] = operator
			publicNameIDRef[nocTableRecord.PublicNameID] = operator
		}
	}

	// NOCLinesRecords
	for _, nocLineRecord := range t.NOCLinesRecords {
		operator := operatorNOCCodeRef[nocLineRecord.NOCCode]

		if operator != nil {
			operator.PrimaryName = nocLineRecord.PublicName
			operator.OtherNames = append(operator.OtherNames, nocLineRecord.PublicName, nocLineRecord.ReferenceName)
			operator.Licence = nocLineRecord.Licence
			operator.TransportType = []string{nocLineRecord.Mode}
		}
	}

	// PublicNameRecords
	websiteRegex, _ := regexp.Compile("#(.+)#")
	for _, publicNameRecord := range t.PublicNameRecords {
		operator := publicNameIDRef[publicNameRecord.PublicNameID]

		if operator != nil {
			operator.PrimaryName = publicNameRecord.OperatorPublicName
			operator.OtherNames = append(operator.OtherNames, publicNameRecord.OperatorPublicName)

			if websiteMatch := websiteRegex.FindStringSubmatch(publicNameRecord.Website); len(websiteMatch) > 1 {
				operator.Website = websiteMatch[1]
			}
			operator.SocialMedia = map[string]string{}

			if publicNameRecord.Twitter != "" {
				operator.SocialMedia["Twitter"] = publicNameRecord.Twitter
			}
			if publicNameRecord.Facebook != "" {
				operator.SocialMedia["Facebook"] = publicNameRecord.Facebook
			}
			if publicNameRecord.YouTube != "" {
				operator.SocialMedia["YouTube"] = publicNameRecord.YouTube
			}

			extractContactDetails(publicNameRecord.LostPropEnq, operator)
			extractContactDetails(publicNameRecord.DisruptEnq, operator)
			extractContactDetails(publicNameRecord.ComplEnq, operator)
			extractContactDetails(publicNameRecord.FareEnq, operator)
			extractContactDetails(publicNameRecord.TTRteEnq, operator)
		}
	}

	// Filter the generated CTDF Operators for duplicate strings
	for _, operator := range operators {
		operator.OtherIdentifiers = util.RemoveDuplicateStrings(operator.OtherIdentifiers, []string{})
		operator.OtherNames = util.RemoveDuplicateStrings(operator.OtherNames, []string{operator.PrimaryName})
	}

	return operators, operatorGroups
}

func (t *TravelineData) ImportIntoMongoAsCTDF(datasource *ctdf.DataSource) {
	datasource.OriginalFormat = "traveline-noc"
	datasource.Provider = "Traveline"
	datasource.Identifier = t.GenerationDate

	log.Info().Msg("Coverting to CTDF")
	operators, operatorGroups := t.convertToCTDF()
	log.Info().Msgf(" - %d Operators", len(operators))
	log.Info().Msgf(" - %d OperatorGroups", len(operatorGroups))

	// Operators table
	operatorsCollection := database.GetCollection("operators")

	// TODO: Doesnt really make sense for the traveline package to be managing CTDF tables and indexes
	operatorIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
	}

	opts := options.CreateIndexes()
	_, err := operatorsCollection.Indexes().CreateMany(context.Background(), operatorIndex, opts)
	if err != nil {
		panic(err)
	}

	// OperatorGroups table
	operatorGroupsCollection := database.GetCollection("operator_groups")

	// TODO: Doesnt really make sense for the traveline package to be managing CTDF tables and indexes
	operatorGroupsIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "identifier", Value: bsonx.Int32(1)}},
		},
	}

	opts = options.CreateIndexes()
	_, err = operatorGroupsCollection.Indexes().CreateMany(context.Background(), operatorGroupsIndex, opts)
	if err != nil {
		panic(err)
	}

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
			operatorOperations := []mongo.WriteModel{}
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
				_, err = operatorsCollection.BulkWrite(context.TODO(), operatorOperations, &options.BulkWriteOptions{})
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

	// Import operator groups
	log.Info().Msg("Importing CTDF OperatorGroups into Mongo")
	var operatorGroupOperationInsert uint64
	var operatorGroupOperationUpdate uint64

	maxBatchSize = int(math.Ceil(float64(len(operatorGroups)) / float64(runtime.NumCPU())))
	numBatches = int(math.Ceil(float64(len(operatorGroups)) / float64(maxBatchSize)))

	processingGroup = sync.WaitGroup{}
	processingGroup.Add(numBatches)

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(operatorGroups) {
			upper = len(operatorGroups)
		}

		batchSlice := operatorGroups[lower:upper]

		go func(operatorGroupsBatch []*ctdf.OperatorGroup) {
			operatorGroupOperations := []mongo.WriteModel{}
			var localOperationInsert uint64
			var localOperationUpdate uint64

			for _, operatorGroup := range operatorGroupsBatch {
				var existingCtdfOperatorGroup *ctdf.OperatorGroup
				operatorGroupsCollection.FindOne(context.Background(), bson.M{"identifier": operatorGroup.Identifier}).Decode(&existingCtdfOperatorGroup)

				if existingCtdfOperatorGroup == nil {
					operatorGroup.CreationDateTime = time.Now()
					operatorGroup.ModificationDateTime = time.Now()
					operatorGroup.DataSource = datasource

					insertModel := mongo.NewInsertOneModel()

					bsonRep, _ := bson.Marshal(operatorGroup)
					insertModel.SetDocument(bsonRep)

					operatorGroupOperations = append(operatorGroupOperations, insertModel)
					localOperationInsert += 1
				} else if existingCtdfOperatorGroup.UniqueHash() != operatorGroup.UniqueHash() {
					operatorGroup.CreationDateTime = existingCtdfOperatorGroup.CreationDateTime
					operatorGroup.ModificationDateTime = time.Now()
					operatorGroup.DataSource = datasource

					updateModel := mongo.NewUpdateOneModel()
					updateModel.SetFilter(bson.M{"identifier": operatorGroup.Identifier})

					bsonRep, _ := bson.Marshal(bson.M{"$set": operatorGroup})
					updateModel.SetUpdate(bsonRep)

					operatorGroupOperations = append(operatorGroupOperations, updateModel)
					localOperationUpdate += 1
				}
			}

			atomic.AddUint64(&operatorGroupOperationInsert, localOperationInsert)
			atomic.AddUint64(&operatorGroupOperationUpdate, localOperationUpdate)

			if len(operatorGroupOperations) > 0 {
				_, err = operatorGroupsCollection.BulkWrite(context.TODO(), operatorGroupOperations, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Err(err).Msg("Failed to bulk write OperatorGroups")
				}
			}

			processingGroup.Done()
		}(batchSlice)
	}

	processingGroup.Wait()

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", operatorGroupOperationInsert)
	log.Info().Msgf(" - %d updates", operatorGroupOperationUpdate)

	log.Info().Msgf("Successfully imported into MongoDB")

	// Send a notification reporting the latest changes
	notify_client.SendEvent("britbus/traveline/import", bson.M{
		"Operators": bson.M{
			"Inserts": operatorOperationInsert,
			"Updates": operatorOperationUpdate,
		},
		"Operator_Groups": bson.M{
			"Inserts": operatorGroupOperationInsert,
			"Updates": operatorGroupOperationUpdate,
		},
	})
}
