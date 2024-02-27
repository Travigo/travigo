package siri_vm

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
	"github.com/travigo/travigo/pkg/redis_client"
	"golang.org/x/net/html/charset"
)

type SiriVMVehicleIdentificationEvent struct {
	VehicleActivity *VehicleActivity
	DataSource      *ctdf.DataSource
	ResponseTime    time.Time
}

type queueEmptyElasticEvent struct {
	Timestamp time.Time
	Duration  int
}

func SubmitToProcessQueue(queue rmq.Queue, vehicle *VehicleActivity, datasource *ctdf.DataSource) bool {
	datasource.OriginalFormat = "siri-vm"

	currentTime := time.Now()

	recordedAtTime, err := time.Parse(ctdf.XSDDateTimeFormat, vehicle.RecordedAtTime)

	if err == nil {
		recordedAtDifference := currentTime.Sub(recordedAtTime)

		// Skip any records that haven't been updated in over 20 minutes
		if recordedAtDifference.Minutes() > 20 {
			return false
		}
	}

	operatorRef := vehicle.MonitoredVehicleJourney.OperatorRef
	vehicleRef := ""
	if vehicle.MonitoredVehicleJourney.VehicleRef != "" {
		vehicleRef = fmt.Sprintf("GB:VEHICLE:%s:%s", operatorRef, vehicle.MonitoredVehicleJourney.VehicleRef)
	}

	vehicleJourneyRef := vehicle.MonitoredVehicleJourney.VehicleJourneyRef
	if vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef != "" {
		vehicleJourneyRef = vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef
	}

	timeframe := vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef
	if timeframe == "" {
		timeframe = currentTime.Format("2006-01-02")
	}

	originRef := fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.OriginRef)
	localJourneyID := fmt.Sprintf(
		"SIRI-VM:LOCALJOURNEYID:%s:%s:%s:%s",
		fmt.Sprintf(ctdf.OperatorNOCFormat, operatorRef),
		vehicle.MonitoredVehicleJourney.LineRef,
		originRef,
		vehicleJourneyRef,
	)

	// Temporary remap of known incorrect values
	// TODO: A better way fof doing this should be done under https://github.com/travigo/travigo/issues/46
	// switch operatorRef {
	// case "SCSO":
	// 	// Stagecoach south (GB:NOCID:137728)
	// 	operatorRef = "SCCO"
	// case "CT4N":
	// 	// CT4n (GB:NOCID:137286)
	// 	operatorRef = "NOCT"
	// case "SCEM":
	// 	// Stagecoach East Midlands (GB:NOCID:136971)
	// 	operatorRef = "SCGR"
	// case "UNO":
	// 	// Uno (GB:NOCID:137967)
	// 	operatorRef = "UNOE"
	// case "SBS":
	// 	// Select Bus Services (GB:NOCID:135680)
	// 	operatorRef = "SLBS"
	// case "BC", "WA", "WB", "WN", "CV", "PB", "YW", "AG", "PN":
	// 	// National Express West Midlands (GB:NOCID:138032)
	// 	operatorRef = "TCVW"
	// }

	locationEvent := vehicletracker.VehicleLocationEvent{
		LocalID: localJourneyID,
		IdentifyingInformation: map[string]string{
			"ServiceNameRef":           vehicle.MonitoredVehicleJourney.LineRef,
			"DirectionRef":             vehicle.MonitoredVehicleJourney.DirectionRef,
			"PublishedLineName":        vehicle.MonitoredVehicleJourney.PublishedLineName,
			"OperatorRef":              fmt.Sprintf(ctdf.OperatorNOCFormat, operatorRef),
			"VehicleJourneyRef":        vehicleJourneyRef,
			"BlockRef":                 vehicle.MonitoredVehicleJourney.BlockRef,
			"OriginRef":                originRef,
			"DestinationRef":           fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.DestinationRef),
			"OriginAimedDepartureTime": vehicle.MonitoredVehicleJourney.OriginAimedDepartureTime,
			"FramedVehicleJourneyDate": vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef,
		},
		SourceType: "siri-vm",
		Location: ctdf.Location{
			Type: "Point",
			Coordinates: []float64{
				vehicle.MonitoredVehicleJourney.VehicleLocation.Longitude,
				vehicle.MonitoredVehicleJourney.VehicleLocation.Latitude,
			},
		},
		Bearing:           vehicle.MonitoredVehicleJourney.Bearing,
		VehicleIdentifier: vehicleRef,
		Timeframe:         timeframe,
		DataSource:        datasource,
		RecordedAt:        recordedAtTime,
	}

	locationEventJson, _ := json.Marshal(locationEvent)

	queue.PublishBytes(locationEventJson)

	return true
}

func ParseXMLFile(reader io.Reader, queue rmq.Queue, datasource *ctdf.DataSource) error {
	var retrievedRecords int64
	var submittedRecords int64

	d := xml.NewDecoder(reader)
	d.CharsetReader = charset.NewReaderLabel
	for {
		tok, err := d.Token()
		if tok == nil || err == io.EOF {
			// EOF means we're done.
			break
		} else if err != nil {
			log.Fatal().Msgf("Error decoding token: %s", err)
			return err
		}

		switch ty := tok.(type) {
		case xml.StartElement:
			if ty.Name.Local == "VehicleActivity" {
				var vehicleActivity VehicleActivity

				if err = d.DecodeElement(&vehicleActivity, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					retrievedRecords += 1

					successfullyPublished := SubmitToProcessQueue(queue, &vehicleActivity, datasource)

					if successfullyPublished {
						submittedRecords += 1
					}
				}
			}
		}
	}

	log.Info().Int64("retrieved", retrievedRecords).Int64("submitted", submittedRecords).Msgf("Parsed latest Siri-VM response")

	// Wait for queue to empty
	startTime := time.Now()
	checkQueueSize()
	executionDuration := time.Since(startTime)
	log.Info().Msgf("Queue took %s to empty", executionDuration.String())

	// Publish stats to Elasticsearch
	elasticEvent, _ := json.Marshal(&queueEmptyElasticEvent{
		Duration:  int(executionDuration.Seconds()),
		Timestamp: time.Now(),
	})

	elastic_client.IndexRequest("sirivm-queue-empty-1", bytes.NewReader(elasticEvent))

	return nil
}

func checkQueueSize() {
	stats, _ := redis_client.QueueConnection.CollectStats([]string{"realtime-queue"})
	inQueue := stats.QueueStats["realtime-queue"].ReadyCount

	if inQueue != 0 {
		time.Sleep(1 * time.Second)

		checkQueueSize()
	}
}
