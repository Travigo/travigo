package railutils

import (
	"context"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func CreateServiceAlert(serviceAlert ctdf.ServiceAlert) {
	serviceAlertCollection := database.GetCollection("service_alerts")

	filter := bson.M{"primaryidentifier": serviceAlert.PrimaryIdentifier}
	update := bson.M{"$set": serviceAlert}
	opts := options.Update().SetUpsert(true)
	serviceAlertCollection.UpdateOne(context.Background(), filter, update, opts)
}

func DeleteServiceAlert(id string) {
	serviceAlertCollection := database.GetCollection("service_alerts")
	filter := bson.M{"primaryidentifier": id}

	serviceAlertCollection.DeleteOne(context.Background(), filter)
}
