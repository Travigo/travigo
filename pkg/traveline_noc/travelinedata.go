package travelinenoc

import (
	"context"
	"fmt"
	"regexp"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/util"
	"github.com/kr/pretty"
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
	operatorsIDs := map[string]int{}
	publicNameIDs := map[string]int{}

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

		ctdfRecord := &ctdf.Operator{
			OtherNames: []string{},
		}
		operators = append(operators, ctdfRecord)
		operatorNOCCodes[nocLineRecord.NOCCode] = len(operators) - 1

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

		operatorsIDs[nocTableRecord.OperatorID] = index
		publicNameIDs[nocTableRecord.PublicNameID] = index
	}

	// OperatorsRecords
	for i := 0; i < len(t.OperatorsRecords); i++ {
		operatorRecord := t.OperatorsRecords[i]
		var ctdfRecord *ctdf.Operator
		// var index int
		operators, ctdfRecord, _ = findOrCreateCTDFRecord(operators, operatorsIDs, operatorRecord.OperatorID)

		ctdfRecord.OtherNames = append(ctdfRecord.OtherNames, operatorRecord.OperatorName)

		if operatorRecord.ManagementDivisionID != "" {
			groupID := mgmtDivisionGroupIDs[operatorRecord.ManagementDivisionID]

			ctdfRecord.OperatorGroup = fmt.Sprintf("UK:NOCGRPID:%s", groupID)
		}
	}

	// PublicNameRecords
	websiteRegex, _ := regexp.Compile("#(.+)#")
	for i := 0; i < len(t.PublicNameRecords); i++ {
		publicNameRecord := t.PublicNameRecords[i]
		var ctdfRecord *ctdf.Operator
		operators, ctdfRecord, _ = findOrCreateCTDFRecord(operators, publicNameIDs, publicNameRecord.PublicNameID)

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
	for _, operator := range operators {
		var existingCtdfOperator *ctdf.Operator
		operatorsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": operator.PrimaryIdentifier}).Decode(&existingCtdfOperator)
		bsonRep, _ := bson.Marshal(operator)

		if existingCtdfOperator == nil {
			_, err := operatorsCollection.InsertOne(context.Background(), bsonRep)

			if err != nil {
				pretty.Println(err)
			}

			continue
		} else {
			_, err := operatorsCollection.ReplaceOne(context.Background(), bson.M{"primaryidentifier": operator.PrimaryIdentifier}, bsonRep)

			if err != nil {
				pretty.Println(err)
			}
		}
	}
	log.Info().Msg("Imported CTDF Operators into Mongo")

	// Import operator groups
	log.Info().Msg("Importing CTDF OperatorGroups into Mongo")
	for _, operatorGroup := range operatorGroups {
		var existingCtdfOperatorGroup *ctdf.OperatorGroup
		operatorGroupsCollection.FindOne(context.Background(), bson.M{"identifier": operatorGroup.Identifier}).Decode(&existingCtdfOperatorGroup)
		bsonRep, _ := bson.Marshal(operatorGroup)

		if existingCtdfOperatorGroup == nil {
			_, err := operatorGroupsCollection.InsertOne(context.Background(), bsonRep)

			if err != nil {
				pretty.Println(err)
			}

			continue
		} else {
			_, err := operatorGroupsCollection.ReplaceOne(context.Background(), bson.M{"identifier": operatorGroup.Identifier}, bsonRep)

			if err != nil {
				pretty.Println(err)
			}
		}
	}
	log.Info().Msg("Imported CTDF OperatorGroups into Mongo")
}
