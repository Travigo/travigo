package siri_vm

import (
	"encoding/json"
	"time"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/rs/zerolog/log"
)

const numConsumers = 5

var identificationQueue chan *SiriVMVehicleIdentificationEvent = make(chan *SiriVMVehicleIdentificationEvent, 2000)

type SiriVM struct {
	ServiceDelivery struct {
		ResponseTimestamp string
		ProducerRef       string

		VehicleMonitoringDelivery struct {
			ResponseTimestamp     string
			RequestMessageRef     string
			ValidUntil            string
			ShortestPossibleCycle string

			VehicleActivity []*VehicleActivity
		}
	}
}

type SiriVMVehicleIdentificationEvent struct {
	VehicleActivity *VehicleActivity
	DataSource      *ctdf.DataSource
	ResponseTime    time.Time
}

func (s *SiriVM) SubmitToProcessQueue(queue rmq.Queue, datasource *ctdf.DataSource) {
	datasource.OriginalFormat = "siri-vm"
	log.Info().Msgf("Submitting the %d activity records in %s to processing queue", len(s.ServiceDelivery.VehicleMonitoringDelivery.VehicleActivity), s.ServiceDelivery.VehicleMonitoringDelivery.RequestMessageRef)

	currentTime := time.Now()

	// Offset the response to the correct current timezone
	responseTimeNoOffset, _ := time.Parse(ctdf.XSDDateTimeWithFractionalFormat, s.ServiceDelivery.ResponseTimestamp)
	responseTime := responseTimeNoOffset.In(currentTime.Location())

	for _, vehicle := range s.ServiceDelivery.VehicleMonitoringDelivery.VehicleActivity {
		recordedAtTime, err := time.Parse(ctdf.XSDDateTimeFormat, vehicle.RecordedAtTime)

		if err == nil {
			recordedAtDifference := currentTime.Sub(recordedAtTime)

			// Skip any records that haven't been updated in over 20 minutes
			if recordedAtDifference.Minutes() > 20 {
				continue
			}
		}

		identificationEvent := &SiriVMVehicleIdentificationEvent{
			VehicleActivity: vehicle,
			ResponseTime:    responseTime,
			DataSource:      datasource,
		}

		identificationEventJson, _ := json.Marshal(identificationEvent)

		queue.PublishBytes(identificationEventJson)
	}
}
