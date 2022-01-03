package travelinenoc

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/util"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

type TravelineData struct {
	NOCLinesRecords            []NOCLinesRecord
	NOCTableRecords            []NOCTableRecord
	OperatorsRecords           []OperatorsRecord
	GroupsRecords              []GroupsRecord
	ManagementDivisionsRecords []ManagementDivisionsRecord
	PublicNameRecords          []PublicNameRecord
}

func findOrCreateCTDFRecord(operators []*ctdf.Operator, idMap map[string]int, identifier string) ([]*ctdf.Operator, *ctdf.Operator, int) {
	if val, ok := idMap[identifier]; ok {
		ctdfRecord := operators[val]

		return operators, ctdfRecord, idMap[identifier]
	} else {
		ctdfRecord := &ctdf.Operator{
			OtherNames: []string{},
		}
		operators = append(operators, ctdfRecord)

		newID := len(operators) - 1
		idMap[identifier] = newID

		return operators, ctdfRecord, newID
	}
}
func findManyOrCreateCTDFRecord(operators []*ctdf.Operator, idMap map[string][]int, identifier string) ([]*ctdf.Operator, []*ctdf.Operator) {
	if val, ok := idMap[identifier]; ok {
		var ctdfRecords []*ctdf.Operator

		for _, id := range val {
			ctdfRecords = append(ctdfRecords, operators[id])
		}

		return operators, ctdfRecords
	} else {
		ctdfRecord := &ctdf.Operator{
			OtherNames: []string{},
		}
		operators = append(operators, ctdfRecord)

		newID := len(operators) - 1
		idMap[identifier] = append(idMap[identifier], newID)

		return operators, []*ctdf.Operator{ctdfRecord}
	}
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
	operatorNOCCodes := map[string]int{}
	operatorsIDs := map[string][]int{}
	publicNameIDs := map[string][]int{}

	// GroupsRecords
	for i := 0; i < len(t.GroupsRecords); i++ {
		groupRecord := t.GroupsRecords[i]

		ctdfRecord := &ctdf.OperatorGroup{
			Identifier: fmt.Sprintf("UK:NOCGRPID:%s", groupRecord.GroupID),
			Name:       groupRecord.GroupName,
		}
		operatorGroups = append(operatorGroups, ctdfRecord)
	}

	// ManagementDivisionsRecord
	for i := 0; i < len(t.ManagementDivisionsRecords); i++ {
		mgmtDivisionRecord := t.ManagementDivisionsRecords[i]

		mgmtDivisionGroupIDs[mgmtDivisionRecord.ManagementDivisionID] = mgmtDivisionRecord.GroupID
	}

	// NOCLinesRecords
	for i := 0; i < len(t.NOCLinesRecords); i++ {
		nocLineRecord := t.NOCLinesRecords[i]

		var ctdfRecord *ctdf.Operator
		operators, ctdfRecord, _ = findOrCreateCTDFRecord(operators, operatorNOCCodes, nocLineRecord.NOCCode)

		ctdfRecord.PrimaryIdentifier = fmt.Sprintf("UK:NOC:%s", nocLineRecord.NOCCode)
		ctdfRecord.PrimaryName = nocLineRecord.PublicName
		ctdfRecord.OtherNames = append(ctdfRecord.OtherNames, nocLineRecord.PublicName, nocLineRecord.ReferenceName)
		ctdfRecord.Licence = nocLineRecord.Licence
		ctdfRecord.TransportType = []string{nocLineRecord.Mode}
	}

	// NOCTableRecords
	for i := 0; i < len(t.NOCTableRecords); i++ {
		nocTableRecord := t.NOCTableRecords[i]
		var ctdfRecord *ctdf.Operator
		var index int
		operators, ctdfRecord, index = findOrCreateCTDFRecord(operators, operatorNOCCodes, nocTableRecord.NOCCode)

		ctdfRecord.OtherNames = append(ctdfRecord.OtherNames, nocTableRecord.OperatorPublicName, nocTableRecord.VOSA_PSVLicenseName)

		operatorsIDs[nocTableRecord.OperatorID] = append(operatorsIDs[nocTableRecord.OperatorID], index)
		publicNameIDs[nocTableRecord.PublicNameID] = append(publicNameIDs[nocTableRecord.PublicNameID], index)
	}

	// OperatorsRecords
	for i := 0; i < len(t.OperatorsRecords); i++ {
		operatorRecord := t.OperatorsRecords[i]
		var ctdfRecords []*ctdf.Operator
		operators, ctdfRecords = findManyOrCreateCTDFRecord(operators, operatorsIDs, operatorRecord.OperatorID)

		for _, ctdfRecord := range ctdfRecords {
			ctdfRecord.OtherNames = append(ctdfRecord.OtherNames, operatorRecord.OperatorName)

			if operatorRecord.ManagementDivisionID != "" {
				groupID := mgmtDivisionGroupIDs[operatorRecord.ManagementDivisionID]

				ctdfRecord.OperatorGroup = fmt.Sprintf("UK:NOCGRPID:%s", groupID)
			}
		}
	}

	// PublicNameRecords
	websiteRegex, _ := regexp.Compile("#(.+)#")
	for i := 0; i < len(t.PublicNameRecords); i++ {
		publicNameRecord := t.PublicNameRecords[i]
		var ctdfRecords []*ctdf.Operator
		operators, ctdfRecords = findManyOrCreateCTDFRecord(operators, publicNameIDs, publicNameRecord.PublicNameID)

		for _, ctdfRecord := range ctdfRecords {
			ctdfRecord.PrimaryName = publicNameRecord.OperatorPublicName
			ctdfRecord.OtherNames = append(ctdfRecord.OtherNames, publicNameRecord.OperatorPublicName)

			if websiteMatch := websiteRegex.FindStringSubmatch(publicNameRecord.Website); len(websiteMatch) > 1 {
				ctdfRecord.Website = websiteMatch[1]
			}
			ctdfRecord.SocialMedia = map[string]string{}

			if publicNameRecord.Twitter != "" {
				ctdfRecord.SocialMedia["Twitter"] = publicNameRecord.Twitter
			}
			if publicNameRecord.Facebook != "" {
				ctdfRecord.SocialMedia["Facebook"] = publicNameRecord.Facebook
			}
			if publicNameRecord.YouTube != "" {
				ctdfRecord.SocialMedia["YouTube"] = publicNameRecord.YouTube
			}

			extractContactDetails(publicNameRecord.LostPropEnq, ctdfRecord)
			extractContactDetails(publicNameRecord.DisruptEnq, ctdfRecord)
			extractContactDetails(publicNameRecord.ComplEnq, ctdfRecord)
			extractContactDetails(publicNameRecord.FareEnq, ctdfRecord)
			extractContactDetails(publicNameRecord.TTRteEnq, ctdfRecord)
		}
	}

	// Filter the generated CTDF Operators
	filteredOperators := []*ctdf.Operator{}
	for _, operator := range operators {
		operator.OtherNames = util.RemoveDuplicateStrings(operator.OtherNames, []string{operator.PrimaryName})

		if operator.PrimaryIdentifier != "" {
			filteredOperators = append(filteredOperators, operator)
		}
	}

	return filteredOperators, operatorGroups
}

func (t *TravelineData) ImportIntoMongoAsCTDF() {
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
	operatorOperations := []mongo.WriteModel{}
	operatorOperationInsert := 0
	operatorOperationUpdate := 0

	maxBatchSize := int(len(operators) / runtime.NumCPU())
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
			for _, operator := range operatorsBatch {
				var existingCtdfOperator *ctdf.Operator
				operatorsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": operator.PrimaryIdentifier}).Decode(&existingCtdfOperator)

				if existingCtdfOperator == nil {
					operator.CreationDateTime = time.Now()
					operator.ModificationDateTime = time.Now()

					insertModel := mongo.NewInsertOneModel()

					bsonRep, _ := bson.Marshal(operator)
					insertModel.SetDocument(bsonRep)

					operatorOperations = append(operatorOperations, insertModel)

					operatorOperationInsert += 1
				} else if existingCtdfOperator.UniqueHash() != operator.UniqueHash() {
					operator.CreationDateTime = existingCtdfOperator.CreationDateTime
					operator.ModificationDateTime = time.Now()

					updateModel := mongo.NewUpdateOneModel()
					updateModel.SetFilter(bson.M{"primaryidentifier": operator.PrimaryIdentifier})

					bsonRep, _ := bson.Marshal(bson.M{"$set": operator})
					updateModel.SetUpdate(bsonRep)

					operatorOperations = append(operatorOperations, updateModel)

					operatorOperationUpdate += 1
				}
			}
			processingGroup.Done()
		}(batchSlice)
	}

	processingGroup.Wait()

	log.Info().Msgf(" - %d inserts", operatorOperationInsert)
	log.Info().Msgf(" - %d updates", operatorOperationUpdate)

	if len(operatorOperations) > 0 {
		_, err = operatorsCollection.BulkWrite(context.TODO(), operatorOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Operators")
		}
		log.Info().Msg(" - Written to MongoDB")
	}

	// Import operator groups
	log.Info().Msg("Importing CTDF OperatorGroups into Mongo")
	operatorGroupOperations := []mongo.WriteModel{}
	operatorGroupOperationInsert := 0
	operatorGroupOperationUpdate := 0

	maxBatchSize = int(len(operatorGroups) / runtime.NumCPU())
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
			for _, operatorGroup := range operatorGroupsBatch {
				var existingCtdfOperatorGroup *ctdf.OperatorGroup
				operatorGroupsCollection.FindOne(context.Background(), bson.M{"identifier": operatorGroup.Identifier}).Decode(&existingCtdfOperatorGroup)

				if existingCtdfOperatorGroup == nil {
					operatorGroup.CreationDateTime = time.Now()
					operatorGroup.ModificationDateTime = time.Now()

					insertModel := mongo.NewInsertOneModel()

					bsonRep, _ := bson.Marshal(operatorGroup)
					insertModel.SetDocument(bsonRep)

					operatorGroupOperations = append(operatorGroupOperations, insertModel)

					operatorGroupOperationInsert += 1
				} else if existingCtdfOperatorGroup.UniqueHash() != operatorGroup.UniqueHash() {
					operatorGroup.CreationDateTime = existingCtdfOperatorGroup.CreationDateTime
					operatorGroup.ModificationDateTime = time.Now()

					updateModel := mongo.NewUpdateOneModel()
					updateModel.SetFilter(bson.M{"identifier": operatorGroup.Identifier})

					bsonRep, _ := bson.Marshal(bson.M{"$set": operatorGroup})
					updateModel.SetUpdate(bsonRep)

					operatorGroupOperations = append(operatorGroupOperations, updateModel)

					operatorGroupOperationUpdate += 1
				}
			}
			processingGroup.Done()
		}(batchSlice)
	}

	processingGroup.Wait()

	log.Info().Msgf(" - %d inserts", operatorGroupOperationInsert)
	log.Info().Msgf(" - %d updates", operatorGroupOperationUpdate)

	if len(operatorGroupOperations) > 0 {
		_, err = operatorGroupsCollection.BulkWrite(context.TODO(), operatorGroupOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write OperatorGroups")
		}
		log.Info().Msg(" - Written to MongoDB")
	}
}
