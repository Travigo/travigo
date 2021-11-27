package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoInstance contains the Mongo client and database objects
type MongoInstance struct {
	Client   *mongo.Client
	Database *mongo.Database
}

var mongoDb MongoInstance

// Database settings (insert your own database name and connection URI)
const dbName = "britbus"
const mongoURI = "mongodb://localhost:27017/" + dbName

func Connect() error {
	client, err := mongo.NewClient(options.Client().ApplyURI(mongoURI))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = client.Connect(ctx)
	database := client.Database(dbName)

	if err != nil {
		return err
	}

	mongoDb = MongoInstance{
		Client:   client,
		Database: database,
	}

	return nil
}

func GetCollection(collectionName string) *mongo.Collection {
	return mongoDb.Database.Collection(collectionName)
}
