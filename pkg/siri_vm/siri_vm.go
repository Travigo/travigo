package siri_vm

import (
	"encoding/json"
	"time"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/ctdf"
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
