package ctdf

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const OperatorGroupIDFormat = "GB:NOCGRPID:%s"

type OperatorGroup struct {
	Identifier string
	Name       string

	DataSource *DataSource

	Operators []*Operator `bson:"-"`

	CreationDateTime     time.Time
	ModificationDateTime time.Time
}

func (group *OperatorGroup) GetReferences() {
	group.GetOperators()
}
func (group *OperatorGroup) GetOperators() {
	operatorsCollection := database.GetCollection("operators")
	cursor, _ := operatorsCollection.Find(context.Background(), bson.M{"operatorgroupref": group.Identifier})

	for cursor.Next(context.TODO()) {
		var operator *Operator
		err := cursor.Decode(&operator)
		if err != nil {
			log.Fatal(err)
		}

		group.Operators = append(group.Operators, operator)
	}
}

func (operatorGroup *OperatorGroup) UniqueHash() string {
	hash := sha256.New()

	hash.Write([]byte(fmt.Sprintf("%s %s",
		operatorGroup.Identifier,
		operatorGroup.Name,
	)))

	return fmt.Sprintf("%x", hash.Sum(nil))
}
