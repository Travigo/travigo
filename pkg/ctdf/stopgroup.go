package ctdf

import (
	"context"
	"log"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const StopGroupIDFormat = "GB:STOPGRP:%s"

type StopGroup struct {
	PrimaryIdentifier string   `groups:"basic"`
	OtherIdentifiers  []string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	Name   string `groups:"basic"`
	Type   string `groups:"basic"`
	Status string `groups:"basic"`

	Stops []Stop `bson:"-" groups:"detailed"`
}

func (stopGroup *StopGroup) GetStops() {
	stopsCollection := database.GetCollection("stops_raw")
	cursor, _ := stopsCollection.Find(context.Background(), bson.M{"associations.associatedidentifier": stopGroup.PrimaryIdentifier})

	for cursor.Next(context.Background()) {
		//Create a value into which the single document can be decoded
		var stop *Stop
		err := cursor.Decode(&stop)
		if err != nil {
			log.Fatal(err)
		}

		stopGroup.Stops = append(stopGroup.Stops, *stop)
	}
}
