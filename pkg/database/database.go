package database

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
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

	err = ConnectRealtime()
	if err != nil {
		return err
	}

	createIndexes()

	runCommands()

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

func ConnectRealtime() error {
	env := util.GetEnvironmentVariables()

	if env["TRAVIGO_REALTIME_MONGODB_CONNECTION"] == "" {
		return nil
	}
	connectionString := env["TRAVIGO_REALTIME_MONGODB_CONNECTION"]

	if env["TRAVIGO_REALTIME_MONGODB_DATABASE"] == "" {
		return nil
	}
	dbName := env["TRAVIGO_REALTIME_MONGODB_DATABASE"]

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(connectionString))

	database := client.Database(dbName)

	if err != nil {
		return err
	}

	RealtimeJourneyInstance = &MongoInstance{
		Client:   client,
		Database: database,
	}

	err = client.Ping(context.Background(), nil)
	if err != nil {
		return err
	}

	return nil
}

func GetInstance(collectionName string) *MongoInstance {
	if collectionName == "realtime_journeys" && RealtimeJourneyInstance != nil {
		return RealtimeJourneyInstance
	} else {
		return Instance
	}
}

func GetCollection(collectionName string) *mongo.Collection {
	return GetInstance(collectionName).Database.Collection(collectionName)
}

// Requires
// use admin
// db.runCommand( {
//    setClusterParameter:
//       { changeStreamOptions: { preAndPostImages: { expireAfterSeconds: 15 } } }
// } )

func runCommands() {
	var result bson.M
	err := GetInstance("realtime_journeys").Database.RunCommand(context.Background(), bson.D{
		{Key: "collMod", Value: "realtime_journeys"},
		{Key: "changeStreamPreAndPostImages", Value: bson.M{"enabled": true}},
	}).Decode(&result)

	if err != nil {
		log.Error().Err(err).Msg("Run commands mongodb")
	}
}
