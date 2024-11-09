package vehicletracker

import (
	"errors"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func (consumer *BatchConsumer) updateServiceAlert(journeyID string, stopID string, serviceID string, vehicleUpdateEvent *VehicleUpdateEvent) (mongo.WriteModel, error) {
	primaryIdentifier := fmt.Sprintf(
		"%s-%s-%d-%d",
		vehicleUpdateEvent.DataSource.DatasetID, vehicleUpdateEvent.ServiceAlertUpdate.Type,
		vehicleUpdateEvent.ServiceAlertUpdate.ValidFrom.UnixMilli(),
		vehicleUpdateEvent.ServiceAlertUpdate.ValidUntil.UnixMilli(),
	)
	var matchedIdentifiers []string

	if journeyID != "" {
		matchedIdentifiers = append(matchedIdentifiers, journeyID)
		primaryIdentifier = fmt.Sprintf("%s-%s", primaryIdentifier, journeyID)
	}

	if stopID != "" {
		matchedIdentifiers = append(matchedIdentifiers, stopID)
		primaryIdentifier = fmt.Sprintf("%s-%s", primaryIdentifier, stopID)
	}

	if serviceID != "" {
		matchedIdentifiers = append(matchedIdentifiers, serviceID)
		primaryIdentifier = fmt.Sprintf("%s-%s", primaryIdentifier, serviceID)
	}

	if len(matchedIdentifiers) == 0 {
		return nil, errors.New("No matching identifiers")
	}

	serviceAlert := ctdf.ServiceAlert{
		PrimaryIdentifier:    primaryIdentifier,
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
	updateModel.SetFilter(bson.M{"primaryidentifier": primaryIdentifier})
	updateModel.SetUpdate(bsonRep)
	updateModel.SetUpsert(true)

	return updateModel, nil
}
