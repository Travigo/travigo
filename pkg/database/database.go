package database

import (
	"context"
	"time"

	"github.com/britbus/britbus/pkg/util"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoInstance struct {
	Client   *mongo.Client
	Database *mongo.Database
}

var Instance *MongoInstance

const defaultConnectionString = "mongodb://localhost:27017/"
const defaultDatabase = "britbus"

func Connect() error {
	connectionString := defaultConnectionString
	dbName := defaultDatabase

	env := util.GetEnvironmentVariables()

	if env["BRITBUS_MONGODB_CONNECTION"] != "" {
		connectionString = env["BRITBUS_MONGODB_CONNECTION"]
	}

	if env["BRITBUS_MONGODB_DATABASE"] != "" {
		dbName = env["BRITBUS_MONGODB_DATABASE"]
	}

	client, err := mongo.NewClient(options.Client().ApplyURI(connectionString))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = client.Connect(ctx)
	database := client.Database(dbName)

	if err != nil {
		return err
	}

	Instance = &MongoInstance{
		Client:   client,
		Database: database,
	}

	err = client.Ping(context.TODO(), nil)
	if err != nil {
		return err
	}

	createIndexes()

	return nil
}

func GetCollection(collectionName string) *mongo.Collection {
	return Instance.Database.Collection(collectionName)
}
