package database

import (
	"context"
	"time"

	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoInstance struct {
	Client   *mongo.Client
	Database *mongo.Database
}

var Instance *MongoInstance
var RealtimeJourneyInstance *MongoInstance

const defaultConnectionString = "mongodb://localhost:27017/"
const defaultDatabase = "travigo"

func Connect() error {
	err := ConnectStandard()
	if err != nil {
		return err
	}

	createIndexes()

	return nil
}

func ConnectStandard() error {
	connectionString := defaultConnectionString
	dbName := defaultDatabase

	env := util.GetEnvironmentVariables()

	if env["TRAVIGO_MONGODB_CONNECTION"] != "" {
		connectionString = env["TRAVIGO_MONGODB_CONNECTION"]
	}

	if env["TRAVIGO_MONGODB_DATABASE"] != "" {
		dbName = env["TRAVIGO_MONGODB_DATABASE"]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(connectionString))

	database := client.Database(dbName)

	if err != nil {
		return err
	}

	Instance = &MongoInstance{
		Client:   client,
		Database: database,
	}

	err = client.Ping(context.Background(), nil)
	if err != nil {
		return err
	}

	return nil
}

func GetCollection(collectionName string) *mongo.Collection {
	return Instance.Database.Collection(collectionName)
}

// Requires
// use admin
// db.runCommand( {
//    setClusterParameter:
//       { changeStreamOptions: { preAndPostImages: { expireAfterSeconds: 15 } } }
// } )
