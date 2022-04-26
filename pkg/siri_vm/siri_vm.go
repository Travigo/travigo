package siri_vm

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"time"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/redis_client"
	"github.com/rs/zerolog/log"
)

const numConsumers = 5

var identificationQueue chan *SiriVMVehicleIdentificationEvent = make(chan *SiriVMVehicleIdentificationEvent, 2000)

type SiriVMVehicleIdentificationEvent struct {
	VehicleActivity *VehicleActivity
	DataSource      *ctdf.DataSource
	ResponseTime    time.Time
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

	identificationEvent := &SiriVMVehicleIdentificationEvent{
		VehicleActivity: vehicle,
		ResponseTime:    currentTime,
		DataSource:      datasource,
	}

	identificationEventJson, _ := json.Marshal(identificationEvent)

	queue.PublishBytes(identificationEventJson)

	return true
}

func ParseXMLFile(reader io.Reader, queue rmq.Queue, datasource *ctdf.DataSource) error {
	var retrievedRecords int64
	var submittedRecords int64

	d := xml.NewDecoder(reader)
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

	// Prevent queue from growing too large
	stats, _ := redis_client.QueueConnection.CollectStats([]string{"realtime-queue"})
	inQueue := stats.QueueStats["realtime-queue"].ReadyCount

	if inQueue > 2*submittedRecords {
		log.Info().Int64("queueSize", inQueue).Msgf("Queue size too large, sleeping")
		time.Sleep(2 * time.Minute)
	}

	return nil
}
