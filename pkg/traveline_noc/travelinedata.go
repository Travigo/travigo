package travelinenoc

import (
	"context"
	"fmt"
	"regexp"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/util"
	"github.com/kr/pretty"
	"go.mongodb.org/mongo-driver/bson"
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

func (t *TravelineData) convertToCTDF() []*ctdf.Operator {
	operators := []*ctdf.Operator{}

	operatorNOCCodes := map[string]int{}
	operatorsIDs := map[string]int{}
	publicNameIDs := map[string]int{}
	maintenanceDivisionIDs := map[string]int{}

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
		var index int
		operators, ctdfRecord, index = findOrCreateCTDFRecord(operators, operatorsIDs, operatorRecord.OperatorID)

		ctdfRecord.OtherNames = append(ctdfRecord.OtherNames, operatorRecord.OperatorName)

		maintenanceDivisionIDs[operatorRecord.ManagementDivisionID] = index
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
	}

	// Filter the generated CTDF Operators
	filteredOperators := []*ctdf.Operator{}
	for _, operator := range operators {
		operator.OtherNames = util.RemoveDuplicateStrings(operator.OtherNames, []string{operator.PrimaryName})

		if operator.PrimaryIdentifier != "" {
			filteredOperators = append(filteredOperators, operator)
		}
	}

	return filteredOperators
}

func (t *TravelineData) ImportIntoMongoAsCTDF() {
	operators := t.convertToCTDF()

	operatorsCollection := database.GetCollection("operators")

	for i := 0; i < len(operators); i++ {
		operator := operators[i]

		var existingCtdfOperator *ctdf.Operator
		operatorsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": operator.PrimaryIdentifier}).Decode(&existingCtdfOperator)

		if existingCtdfOperator == nil {
			bsonRep, _ := bson.Marshal(operator)
			_, err := operatorsCollection.InsertOne(context.Background(), bsonRep)

			if err != nil {
				pretty.Println(err)
			}

			continue
		} else {
			bsonRep, _ := bson.Marshal(operator)
			_, err := operatorsCollection.ReplaceOne(context.Background(), bson.M{"primaryidentifier": operator.PrimaryIdentifier}, bsonRep)

			if err != nil {
				pretty.Println(err)
			}
		}
	}

	pretty.Println(operators)
	// log.Println(len(operators))
}
