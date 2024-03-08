package travelinenoc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/formats"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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
	var operators []*ctdf.Operator
	var operatorGroups []*ctdf.OperatorGroup

	groupExists := map[string]bool{}
	mgmtDivisionGroupIDs := map[string]string{}
	operatorNOCCodeRef := map[string]*ctdf.Operator{}
	operatorsIDRef := map[string]*ctdf.Operator{}
	publicNameIDRef := map[string]*ctdf.Operator{}

	// GroupsRecords
	noGroupRegex, _ := regexp.Compile("NoGroup_*")
	for _, groupRecord := range t.GroupsRecords {
		if !noGroupRegex.Match([]byte(groupRecord.GroupName)) {
			ctdfRecord := &ctdf.OperatorGroup{
				Identifier: fmt.Sprintf(ctdf.OperatorGroupIDFormat, groupRecord.GroupID),
				Name:       groupRecord.GroupName,
			}
			operatorGroups = append(operatorGroups, ctdfRecord)

			groupExists[groupRecord.GroupID] = true
		}
	}

	// ManagementDivisionsRecord
	for _, mgmtDivisionRecord := range t.ManagementDivisionsRecords {
		// Create a reference between the Management Division ID and the Group ID
		if groupExists[mgmtDivisionRecord.GroupID] {
			mgmtDivisionGroupIDs[mgmtDivisionRecord.ManagementDivisionID] = mgmtDivisionRecord.GroupID
		}
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

			if groupID != "" {
				ctdfRecord.OperatorGroupRef = fmt.Sprintf(ctdf.OperatorGroupIDFormat, groupID)
			}
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
			operator.OtherNames = append(operator.OtherNames, nocTableRecord.OperatorPublicName, nocTableRecord.VOSAPSVLicenseName)

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
			operator.TransportType = nocLineRecord.Mode

			if nocLineRecord.London != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:LONDON")
			}
			if nocLineRecord.SouthWest != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:SOUTHWEST")
			}
			if nocLineRecord.WestMidlands != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:WESTMIDLANDS")
			}
			if nocLineRecord.Wales != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:WALES")
			}
			if nocLineRecord.Yorkshire != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:YORKSHIRE")
			}
			if nocLineRecord.NorthWest != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:NORTHWEST")
			}
			if nocLineRecord.NorthEast != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:NORTHEAST")
			}
			if nocLineRecord.Scotland != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:SCOTLAND")
			}
			if nocLineRecord.SouthEast != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:SOUTHEAST")
			}
			if nocLineRecord.EastAnglia != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:EASTANGLIA")
			}
			if nocLineRecord.EastMidlands != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:EASTMIDLANDS")
			}
			if nocLineRecord.NorthernIreland != "" {
				operator.Regions = append(operator.Regions, "UK:REGION:NORTHERNIRELAND")
			}
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

func (t *TravelineData) ImportIntoMongoAsCTDF(datasetid string, supportedObjects formats.SupportedObjects, datasource *ctdf.DataSource) error {
	if !supportedObjects.Operators || !supportedObjects.OperatorGroups {
		return errors.New("This format requires operators & operatorgroups to be enabled")
	}

	log.Info().Msg("Converting to CTDF")
	operators, operatorGroups := t.convertToCTDF()
	log.Info().Msgf(" - %d Operators", len(operators))
	log.Info().Msgf(" - %d OperatorGroups", len(operatorGroups))

	// Operators table
	operatorsCollection := database.GetCollection("operators")

	// OperatorGroups table
	operatorGroupsCollection := database.GetCollection("operator_groups")

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

	// Import operator groups
	log.Info().Msg("Importing CTDF OperatorGroups into Mongo")
	var operatorGroupOperationInsert uint64

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
			var operatorGroupOperations []mongo.WriteModel
			var localOperationInsert uint64

			for _, operatorGroup := range operatorGroupsBatch {
				operatorGroup.CreationDateTime = time.Now()
				operatorGroup.ModificationDateTime = time.Now()
				operatorGroup.DataSource = datasource

				bsonRep, _ := bson.Marshal(bson.M{"$set": operatorGroup})
				updateModel := mongo.NewUpdateOneModel()
				updateModel.SetFilter(bson.M{"identifier": operatorGroup.Identifier})
				updateModel.SetUpdate(bsonRep)
				updateModel.SetUpsert(true)

				operatorGroupOperations = append(operatorGroupOperations, updateModel)
				localOperationInsert += 1
			}

			atomic.AddUint64(&operatorGroupOperationInsert, localOperationInsert)

			if len(operatorGroupOperations) > 0 {
				_, err := operatorGroupsCollection.BulkWrite(context.Background(), operatorGroupOperations, &options.BulkWriteOptions{})
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

	log.Info().Msgf("Successfully imported into MongoDB")

	return nil
}
