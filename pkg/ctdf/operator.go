package ctdf

import (
	"crypto/sha256"
	"fmt"
	"time"
)

const OperatorIDFormat = "GB:NOC:%s"

type Operator struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	PrimaryName string   `groups:"basic"`
	OtherNames  []string `groups:"basic"`

	OperatorGroupRef string `groups:"detailed"`

	TransportType []string `groups:"detailed"`

	Licence string `groups:"detailed"`

	Website     string            `groups:"detailed"`
	Email       string            `groups:"detailed"`
	Address     string            `groups:"detailed"`
	PhoneNumber string            `groups:"detailed"`
	SocialMedia map[string]string `groups:"detailed"`
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
