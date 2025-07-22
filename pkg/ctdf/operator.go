package ctdf

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const OperatorNOCFormat = "gb-noc-%s"
const OperatorNOCIDFormat = "gb-nocid-%s"
const OperatorTOCFormat = "gb-toc-%s"

type Operator struct {
	PrimaryIdentifier string   `groups:"basic,departures-llm" bson:",omitempty"`
	OtherIdentifiers  []string `groups:"detailed" bson:",omitempty"`

	CreationDateTime     time.Time `groups:"detailed" bson:",omitempty"`
	ModificationDateTime time.Time `groups:"detailed" bson:",omitempty"`

	DataSource *DataSourceReference `groups:"detailed" bson:",omitempty"`

	PrimaryName string   `groups:"basic,departures-llm" bson:",omitempty"`
	OtherNames  []string `groups:"detailed" bson:",omitempty"`

	OperatorGroupRef string         `groups:"internal" bson:",omitempty"`
	OperatorGroup    *OperatorGroup `groups:"detailed" bson:"-"`

	TransportType TransportType `groups:"detailed" bson:",omitempty"`

	Licence string `groups:"internal" bson:",omitempty"`

	Website     string            `groups:"detailed" bson:",omitempty"`
	Email       string            `groups:"detailed" bson:",omitempty"`
	Address     string            `groups:"detailed" bson:",omitempty"`
	PhoneNumber string            `groups:"detailed" bson:",omitempty"`
	SocialMedia map[string]string `groups:"detailed" bson:",omitempty"`

	Regions []string `groups:"detailed" bson:",omitempty"`
}

func (operator *Operator) GetReferences() {
	operator.GetOperatorGroup()
}
func (operator *Operator) GetOperatorGroup() {
	operatorGroupsCollection := database.GetCollection("operator_groups")
	operatorGroupsCollection.FindOne(context.Background(), bson.M{"identifier": operator.OperatorGroupRef}).Decode(&operator.OperatorGroup)
}

func (operator *Operator) UniqueHash() string {
	hash := sha256.New()

	hash.Write([]byte(fmt.Sprintf("%s %s %s %s %s %s %s %s %s %s %s %s %s",
		operator.PrimaryIdentifier,
		operator.OtherIdentifiers,
		operator.PrimaryName,
		operator.OtherNames,
		operator.OperatorGroupRef,
		operator.TransportType,
		operator.Licence,
		operator.Website,
		operator.Email,
		operator.Address,
		operator.PhoneNumber,
		operator.SocialMedia,
		operator.Regions,
	)))

	return fmt.Sprintf("%x", hash.Sum(nil))
}
