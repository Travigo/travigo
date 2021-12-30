package ctdf

import (
	"crypto/sha256"
	"fmt"
	"time"
)

type Operator struct {
	PrimaryIdentifier string
	OtherIdentifiers  map[string]string

	CreationDateTime     time.Time
	ModificationDateTime time.Time

	DataSource *DataSource

	PrimaryName string
	OtherNames  []string

	OperatorGroup string

	TransportType []string

	Licence string

	// LegalEntity string
	Website     string
	Email       string
	Address     string
	PhoneNumber string
	SocialMedia map[string]string
}

func (operator *Operator) UniqueHash() string {
	hash := sha256.New()

	hash.Write([]byte(fmt.Sprintf("%s %s %s %s %s %s %s %s %s %s %s %s",
		operator.PrimaryIdentifier,
		operator.OtherIdentifiers,
		operator.PrimaryName,
		operator.OtherNames,
		operator.OperatorGroup,
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
