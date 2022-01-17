package ctdf

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const OperatorNOCFormat = "GB:NOC:%s"
const OperatorNOCIDFormat = "GB:NOCID:%s"

type Operator struct {
	PrimaryIdentifier string   `groups:"basic"`
	OtherIdentifiers  []string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	PrimaryName string   `groups:"basic"`
	OtherNames  []string `groups:"basic"`

	OperatorGroupRef string         `groups:"detailed"`
	OperatorGroup    *OperatorGroup `groups:"detailed" bson:"-"`

	TransportType []string `groups:"detailed"`

	Licence string `groups:"detailed"`

	Website     string            `groups:"detailed"`
	Email       string            `groups:"detailed"`
	Address     string            `groups:"detailed"`
	PhoneNumber string            `groups:"detailed"`
	SocialMedia map[string]string `groups:"detailed"`
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

	hash.Write([]byte(fmt.Sprintf("%s %s %s %s %s %s %s %s %s %s %s %s",
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
	)))

	return fmt.Sprintf("%x", hash.Sum(nil))
}
