package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ModeDisruptionTracker struct {
	Mode        TfLMode
	RefreshRate time.Duration
	Service     *ctdf.Service
}

func (d *ModeDisruptionTracker) Run() {
	log.Info().
		Str("mode", d.Mode.ModeID).
		Msg("Registering new mode disruption tracker")

	for {
		startTime := time.Now()

		d.GetDisruptions()

		endTime := time.Now()
		executionDuration := endTime.Sub(startTime)
		waitTime := d.RefreshRate - executionDuration

		if waitTime.Seconds() > 0 {
			time.Sleep(waitTime)
		}
	}
}

func (d *ModeDisruptionTracker) GetDisruptions() {
	now := time.Now()
	datasource := &ctdf.DataSource{
		OriginalFormat: "JSON",
		Provider:       "GB-TfL",
		Dataset:        fmt.Sprintf("StopPoint/mode/%s/Disruption", d.Mode.ModeID),
		Identifier:     fmt.Sprint(now.Unix()),
	}

	requestURL := fmt.Sprintf("https://api.tfl.gov.uk/StopPoint/mode/%s/Disruption?app_key=%s", d.Mode.ModeID, TfLAppKey)
	req, _ := http.NewRequest("GET", requestURL, nil)
	req.Header["user-agent"] = []string{"curl/7.54.1"} // TfL is protected by cloudflare and it gets angry when no user agent is set

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		log.Fatal().Err(err).Msg("Download file")
	}
	defer resp.Body.Close()

	jsonBytes, _ := io.ReadAll(resp.Body)

	var tflDisruptionsData []struct {
		ATCO        string `json:"atcoCode"`
		StationATCO string `json:"stationAtcoCode"`

		FromDate string `json:"fromDate"`
		ToDate   string `json:"toDate"`

		Description string `json:"description"`

		Type       string `json:"type"`
		Appearance string `json:"appearance"`
	}
	json.Unmarshal(jsonBytes, &tflDisruptionsData)

	serviceAlertsCollection := database.GetCollection("service_alerts")
	//var realtimeJourneyUpdateOperations []mongo.WriteModel
	serviceAlertsUpdateOperation := []mongo.WriteModel{}

	for _, tflDisruption := range tflDisruptionsData {
		primaryIdentifier := fmt.Sprintf(
			"GB:TFLDISRUPTION:%s:%s:%s:%s:%s:%s",
			d.Mode.ModeID, tflDisruption.Type, tflDisruption.Appearance, tflDisruption.ATCO, tflDisruption.FromDate, tflDisruption.ToDate,
		)

		searchQuery := bson.M{"primaryidentifier": primaryIdentifier}

		var serviceAlert *ctdf.ServiceAlert

		serviceAlertsCollection.FindOne(context.Background(), searchQuery).Decode(&serviceAlert)

		alertText := strings.ReplaceAll(tflDisruption.Description, "\\n", "")

		if serviceAlert == nil {
			validFrom, _ := time.Parse(time.RFC3339, tflDisruption.FromDate)
			validUntil, _ := time.Parse(time.RFC3339, tflDisruption.ToDate)

			alertType := ctdf.ServiceAlertTypeInformation

			if tflDisruption.Type == "Closure" {
				alertType = ctdf.ServiceAlertTypeStopClosed
			} else if tflDisruption.Appearance == "PlannedWork" {
				alertType = ctdf.ServiceAlertTypePlanned
			}

			serviceAlert = &ctdf.ServiceAlert{
				PrimaryIdentifier: primaryIdentifier,

				DataSource: datasource,

				CreationDateTime:     now,
				ModificationDateTime: now,

				ValidFrom:  validFrom,
				ValidUntil: validUntil,

				AlertType: alertType,
				Text:      alertText,

				MatchedIdentifiers: []string{
					fmt.Sprintf(ctdf.StopIDFormat, tflDisruption.ATCO),
					fmt.Sprintf(ctdf.StopIDFormat, tflDisruption.StationATCO),
				},
			}
		} else {
			serviceAlert.DataSource = datasource
			serviceAlert.ModificationDateTime = now
			serviceAlert.Text = alertText
		}

		bsonRep, _ := bson.Marshal(bson.M{"$set": serviceAlert})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(searchQuery)
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		serviceAlertsUpdateOperation = append(serviceAlertsUpdateOperation, updateModel)
	}

	if len(serviceAlertsUpdateOperation) > 0 {
		_, err := serviceAlertsCollection.BulkWrite(context.TODO(), serviceAlertsUpdateOperation, &options.BulkWriteOptions{})

		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write ServiceAlerts")
		}
	}

	log.Info().
		Str("mode", d.Mode.ModeID).
		Int("length", len(serviceAlertsUpdateOperation)).
		Msg("update mode disruptions")

	// Remove any tfl service alerts that wasnt updated in this run
	// This means its dropped off all the stop arrivals (most likely as its finished)
	deleteQuery := bson.M{
		"datasource.provider":   datasource.Provider,
		"datasource.dataset":    datasource.Dataset,
		"datasource.identifier": bson.M{"$ne": datasource.Identifier},
	}

	deleted, _ := serviceAlertsCollection.DeleteMany(context.Background(), deleteQuery)
	if deleted.DeletedCount > 0 {
		log.Info().
			Str("id", d.Mode.ModeID).
			Int64("length", deleted.DeletedCount).
			Msg("delete expired journeys")
	}
}
