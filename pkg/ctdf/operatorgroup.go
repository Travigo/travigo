package ctdf

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const OperatorGroupIDFormat = "GB:NOCGRPID:%s"

type OperatorGroup struct {
	Identifier string `groups:"basic" gorm:"uniqueIndex"`
	Name       string `groups:"basic"`

	DataSource *DataSource `groups:"internal" gorm:"embedded;embeddedPrefix:datasource_"`

	Operators []*Operator `bson:"-" groups:"detailed" gorm:"-"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`
}

func (group *OperatorGroup) GetReferences() {
	group.GetOperators()
}
func (group *OperatorGroup) GetOperators() {
	operatorsCollection := database.GetCollection("operators")
	cursor, _ := operatorsCollection.Find(context.Background(), bson.M{"operatorgroupref": group.Identifier})

	for cursor.Next(context.Background()) {
		var operator *Operator
		err := cursor.Decode(&operator)
		if err != nil {
			log.Fatal(err)
		}

		group.Operators = append(group.Operators, operator)
	}
}

func (group *OperatorGroup) UniqueHash() string {
	hash := sha256.New()

	hash.Write([]byte(fmt.Sprintf("%s %s",
		group.Identifier,
		group.Name,
	)))

	return fmt.Sprintf("%x", hash.Sum(nil))
}
