package database

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type MongoInstance struct {
	Client   *mongo.Client
	Database *mongo.Database
}

var MongoGlobalInstance *MongoInstance

var GlobalGorm *gorm.DB

const defaultMongoConnectionString = "mongodb://localhost:27017/"
const defaultMongoDatabase = "travigo"

func Connect() error {
	if err := ConnectMongoDB(); err != nil {
		return err
	}

	if err := ConnectPostgres(); err != nil {
		return err
	}

	return nil
}

func ConnectPostgres() error {
	env := util.GetEnvironmentVariables()

	connectionString := "postgres://travigo:password@localhost:5432/travigo"

	if env["TRAVIGO_POSTGRES_CONNECTION"] != "" {
		connectionString = env["TRAVIGO_POSTGRES_CONNECTION"]
	}

	var err error

	GlobalGorm, err = gorm.Open(postgres.Open(connectionString), &gorm.Config{})
	if err != nil {
		return err
	}

	return nil
}

func ConnectMongoDB() error {
	connectionString := defaultMongoConnectionString
	dbName := defaultMongoDatabase

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

	MongoGlobalInstance = &MongoInstance{
		Client:   client,
		Database: database,
	}

	err = client.Ping(context.Background(), nil)
	if err != nil {
		return err
	}

	createIndexes()

	runCommands()

	return nil
}

func GetCollection(collectionName string) *mongo.Collection {
	return MongoGlobalInstance.Database.Collection(collectionName)
}

// Requires
// use admin
// db.runCommand( {
//    setClusterParameter:
//       { changeStreamOptions: { preAndPostImages: { expireAfterSeconds: 15 } } }
// } )

func runCommands() {
	var result bson.M
	err := MongoGlobalInstance.Database.RunCommand(context.Background(), bson.D{
		{Key: "collMod", Value: "realtime_journeys"},
		{Key: "changeStreamPreAndPostImages", Value: bson.M{"enabled": true}},
	}).Decode(&result)

	if err != nil {
		log.Error().Err(err).Msg("Run commands mongodb")
	}
}
