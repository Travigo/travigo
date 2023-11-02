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
	Mode        *TfLMode
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
		d.GetLineStatuses()

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
		validFrom, _ := time.Parse(time.RFC3339, tflDisruption.FromDate)
		validUntil, _ := time.Parse(time.RFC3339, tflDisruption.ToDate)

		if serviceAlert == nil {
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

			serviceAlert.ValidFrom = validFrom
			serviceAlert.ValidUntil = validUntil
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

	d.cleanupOldServiceAlerts(datasource)
}

func (d *ModeDisruptionTracker) GetLineStatuses() {
	now := time.Now()
	datasource := &ctdf.DataSource{
		OriginalFormat: "JSON",
		Provider:       "GB-TfL",
		Dataset:        fmt.Sprintf("Line/Mode/%s/Status", d.Mode.ModeID),
		Identifier:     fmt.Sprint(now.Unix()),
	}

	requestURL := fmt.Sprintf("https://api.tfl.gov.uk/Line/Mode/%s/Status?app_key=%s", d.Mode.ModeID, TfLAppKey)
	req, _ := http.NewRequest("GET", requestURL, nil)
	req.Header["user-agent"] = []string{"curl/7.54.1"} // TfL is protected by cloudflare and it gets angry when no user agent is set

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		log.Fatal().Err(err).Msg("Download file")
	}
	defer resp.Body.Close()

	jsonBytes, _ := io.ReadAll(resp.Body)

	var tflLineStatusData []struct {
		LineID string `json:"id"`

		LineStatuses []struct {
			LineID                    string `json:"lineId"`
			StatusSeverity            int    `json:"statusSeverity"`
			StatusSeverityDescription string `json:"statusSeverityDescription"`
			Reason                    string `json:"reason"`

			ValidityPeriods []struct {
				IsNow bool `json:"isNow"`
			} `json:"validityPeriods"`
		} `json:"lineStatuses"`
	}
	json.Unmarshal(jsonBytes, &tflLineStatusData)

	serviceAlertsCollection := database.GetCollection("service_alerts")
	serviceAlertsUpdateOperation := []mongo.WriteModel{}

	for _, lineStatuses := range tflLineStatusData {
		var service *ctdf.Service

		for _, line := range d.Mode.Lines {
			if line.LineID == lineStatuses.LineID {
				service = line.Service
				break
			}
		}

		if service == nil {
			log.Error().Str("line", lineStatuses.LineID).Msg("Failed to get Service for line status")
			continue
		}

		for _, lineStatusUpdate := range lineStatuses.LineStatuses {
			if lineStatusUpdate.StatusSeverityDescription == "Good Service" || lineStatusUpdate.StatusSeverityDescription == "No Issues" {
				continue
			}

			validNow := false
			for _, validityPeriods := range lineStatusUpdate.ValidityPeriods {
				if validityPeriods.IsNow {
					validNow = true
				}
			}
			if !validNow {
				continue
			}

			// Ignore any closures between 1am-3am
			if lineStatusUpdate.StatusSeverityDescription == "Service Closed" && (now.Hour() >= 1 && now.Hour() <= 3) {
				continue
			}

			// Ignore the daily update about the service closing for the day
			if lineStatusUpdate.StatusSeverityDescription == "Service Closed" &&
				(strings.Contains(lineStatusUpdate.Reason, "Service will resume later this morning") ||
					strings.Contains(lineStatusUpdate.Reason, "Service will resume later.") ||
					strings.Contains(lineStatusUpdate.Reason, "Service will resume at 06:00") ||
					strings.Contains(lineStatusUpdate.Reason, "Service will resume at 06.00") ||
					strings.Contains(lineStatusUpdate.Reason, "The Woolwich Ferry operates a one boat service from 0630 to 1900 on weekdays only.")) {
				continue
			}

			var serviceAlertType ctdf.ServiceAlertType

			switch lineStatusUpdate.StatusSeverityDescription {
			case "Closed", "Suspended", "Planned Closure", "Service Closed":
				serviceAlertType = ctdf.ServiceAlertTypeServiceSuspended
			case "Part Suspended", "Part Closure", "Part Closed":
				serviceAlertType = ctdf.ServiceAlertTypeServicePartSuspended
			case "Severe Delays":
				serviceAlertType = ctdf.ServiceAlertTypeSevereDelays
			case "Reduced Service":
				serviceAlertType = ctdf.ServiceAlertTypeDelays
			case "Minor Delays":
				serviceAlertType = ctdf.ServiceAlertTypeMinorDelays
			case "Bus Service", "Exit Only", "Change of frequency", "Diverted", "Not Running", "Issues Reported":
				serviceAlertType = ctdf.ServiceAlertTypeWarning
			case "No Step Free Access", "Information":
				serviceAlertType = ctdf.ServiceAlertTypeInformation
			default:
				serviceAlertType = ctdf.ServiceAlertTypeInformation
			}

			primaryIdentifier := fmt.Sprintf(
				"GB:TFLLINESTATUS:%s:%d",
				service.PrimaryIdentifier, lineStatusUpdate.StatusSeverity,
			)

			searchQuery := bson.M{"primaryidentifier": primaryIdentifier}

			var serviceAlert *ctdf.ServiceAlert

			serviceAlertsCollection.FindOne(context.Background(), searchQuery).Decode(&serviceAlert)

			// Little hack to keep it always valid
			validFrom := now.Add(-4 * time.Hour)
			validUntil := now.Add(4 * time.Hour)

			if serviceAlert == nil {
				serviceAlert = &ctdf.ServiceAlert{
					PrimaryIdentifier: primaryIdentifier,

					DataSource: datasource,

					CreationDateTime:     now,
					ModificationDateTime: now,

					ValidFrom:  validFrom,
					ValidUntil: validUntil,

					AlertType: serviceAlertType,

					Title: lineStatusUpdate.StatusSeverityDescription,
					Text:  lineStatusUpdate.Reason,

					MatchedIdentifiers: []string{
						service.PrimaryIdentifier,
					},
				}
			} else {
				serviceAlert.DataSource = datasource
				serviceAlert.ModificationDateTime = now
				serviceAlert.Text = lineStatusUpdate.Reason

				serviceAlert.ValidFrom = validFrom
				serviceAlert.ValidUntil = validUntil
			}

			bsonRep, _ := bson.Marshal(bson.M{"$set": serviceAlert})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(searchQuery)
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)

			serviceAlertsUpdateOperation = append(serviceAlertsUpdateOperation, updateModel)
		}
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
		Msg("update mode line status")

	d.cleanupOldServiceAlerts(datasource)
}

func (d *ModeDisruptionTracker) cleanupOldServiceAlerts(datasource *ctdf.DataSource) {
	serviceAlertsCollection := database.GetCollection("service_alerts")

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
