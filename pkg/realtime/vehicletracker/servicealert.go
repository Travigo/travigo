package vehicletracker

import (
	"errors"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func (consumer *BatchConsumer) updateServiceAlert(matchedIdentifiers []string, vehicleUpdateEvent *VehicleUpdateEvent) (mongo.WriteModel, error) {
	if len(matchedIdentifiers) == 0 {
		return nil, errors.New("No matching identifiers")
	}

	serviceAlert := ctdf.ServiceAlert{
		PrimaryIdentifier:    vehicleUpdateEvent.LocalID,
		OtherIdentifiers:     map[string]string{},
		CreationDateTime:     time.Time{},
		ModificationDateTime: vehicleUpdateEvent.RecordedAt,
		DataSource:           vehicleUpdateEvent.DataSource,
		AlertType:            vehicleUpdateEvent.ServiceAlertUpdate.Type,
		Title:                vehicleUpdateEvent.ServiceAlertUpdate.Title,
		Text:                 vehicleUpdateEvent.ServiceAlertUpdate.Description,
		MatchedIdentifiers:   matchedIdentifiers,
		ValidFrom:            vehicleUpdateEvent.ServiceAlertUpdate.ValidFrom,
		ValidUntil:           vehicleUpdateEvent.ServiceAlertUpdate.ValidUntil,
	}

	bsonRep, _ := bson.Marshal(bson.M{"$set": serviceAlert})
	updateModel := mongo.NewUpdateOneModel()
	updateModel.SetFilter(bson.M{"primaryidentifier": vehicleUpdateEvent.LocalID})
	updateModel.SetUpdate(bsonRep)
	updateModel.SetUpsert(true)

	return updateModel, nil
}
