package naptan

import (
	"context"
	"log"

	"github.com/britbus/britbus/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type StopArea struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	StopAreaCode          string
	Name                  string
	AdministrativeAreaRef string
	StopAreaType          string

	Location *Location

	Stops []StopPoint
}

func (stopArea *StopArea) GetStops() {
	stopsCollection := database.GetCollection("stops")
	cursor, _ := stopsCollection.Find(context.Background(), bson.M{"stopareas.stopareacode": stopArea.StopAreaCode})

	for cursor.Next(context.TODO()) {
		//Create a value into which the single document can be decoded
		var stopPoint *StopPoint
		err := cursor.Decode(&stopPoint)
		if err != nil {
			log.Fatal(err)
		}

		stopArea.Stops = append(stopArea.Stops, *stopPoint)
	}
}
