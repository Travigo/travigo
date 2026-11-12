package siri_vm

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
	"github.com/travigo/travigo/pkg/redis_client"
	"golang.org/x/net/html/charset"
)

type SiriVM struct {
	reader io.Reader
	queue  rmq.Queue
}

type SiriVMVehicleIdentificationEvent struct {
	VehicleActivity *VehicleActivity
	DataSource      *ctdf.DataSourceReference
	ResponseTime    time.Time
}

func vehicleUpdate(vehicle *VehicleActivity, dataset datasets.DataSet, datasource *ctdf.DataSourceReference) *vehicletracker.VehicleUpdateEvent {
	datasource.OriginalFormat = "siri-vm"

	currentTime := time.Now()

	recordedAtTime, err := time.Parse(ctdf.XSDDateTimeFormat, vehicle.RecordedAtTime)

	if err == nil {
		recordedAtDifference := currentTime.Sub(recordedAtTime)

		// Skip any records that haven't been updated in over 20 minutes
		if recordedAtDifference.Minutes() > 20 {
			return nil
		}
	}

	operatorRef := vehicle.MonitoredVehicleJourney.OperatorRef
	vehicleRef := ""
	if vehicle.MonitoredVehicleJourney.VehicleRef != "" {
		vehicleRef = vehicle.MonitoredVehicleJourney.VehicleRef //fmt.Sprintf("GB:VEHICLE:%s:%s", operatorRef, vehicle.MonitoredVehicleJourney.VehicleRef)
	}

	vehicleJourneyRef := vehicle.MonitoredVehicleJourney.VehicleJourneyRef
	if vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef != "" {
		vehicleJourneyRef = vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef
	}

	timeframe := vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef
	if timeframe == "" {
		timeframe = currentTime.Format("2006-01-02")
	}

	originRef := fmt.Sprintf(ctdf.GBStopIDFormat, vehicle.MonitoredVehicleJourney.OriginRef)
	localJourneyID := fmt.Sprintf(
		"SIRI-VM:LOCALJOURNEYID:%s:%s:%s:%s:%s:%s:%s",
		fmt.Sprintf(ctdf.OperatorNOCFormat, operatorRef),
		vehicle.MonitoredVehicleJourney.LineRef,
		originRef,
		timeframe,
		vehicleRef,
		vehicleJourneyRef,
		vehicle.MonitoredVehicleJourney.OriginAimedDepartureTime,
	)

	locationEvent := vehicletracker.VehicleUpdateEvent{
		MessageType: vehicletracker.VehicleUpdateEventTypeTrip,
		LocalID:     localJourneyID,
		SourceType:  "siri-vm",
		VehicleLocationUpdate: &vehicletracker.VehicleLocationUpdate{
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

			IdentifyingInformation: map[string]string{
				"ServiceNameRef":           vehicle.MonitoredVehicleJourney.LineRef,
				"DirectionRef":             vehicle.MonitoredVehicleJourney.DirectionRef,
				"PublishedLineName":        vehicle.MonitoredVehicleJourney.PublishedLineName,
				"OperatorRef":              fmt.Sprintf(ctdf.OperatorNOCFormat, operatorRef),
				"VehicleJourneyRef":        vehicleJourneyRef,
				"BlockRef":                 vehicle.MonitoredVehicleJourney.BlockRef,
				"OriginRef":                originRef,
				"DestinationRef":           fmt.Sprintf(ctdf.GBStopIDFormat, vehicle.MonitoredVehicleJourney.DestinationRef),
				"OriginAimedDepartureTime": vehicle.MonitoredVehicleJourney.OriginAimedDepartureTime,
				"FramedVehicleJourneyDate": vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef,
				"LinkedDataset":            dataset.LinkedDataset,
			},
		},
		DataSource: datasource,
		RecordedAt: recordedAtTime,
	}

	// Calculate occupancy
	if vehicle.Extensions.VehicleJourney.SeatedOccupancy != 0 {
		totalCapacity := vehicle.Extensions.VehicleJourney.SeatedCapacity + vehicle.Extensions.VehicleJourney.WheelchairCapacity
		totalOccupancy := vehicle.Extensions.VehicleJourney.SeatedOccupancy + vehicle.Extensions.VehicleJourney.WheelchairOccupancy

		locationEvent.VehicleLocationUpdate.Occupancy = ctdf.RealtimeJourneyOccupancy{
			OccupancyAvailable: true,
			ActualValues:       true,

			Capacity:  totalCapacity,
			Occupancy: totalOccupancy,

			SeatedInformation: true,
			SeatedCapacity:    vehicle.Extensions.VehicleJourney.SeatedCapacity,
			SeatedOccupancy:   vehicle.Extensions.VehicleJourney.SeatedOccupancy,

			WheelchairInformation: true,
			WheelchairCapacity:    vehicle.Extensions.VehicleJourney.WheelchairCapacity,
			WheelchairOccupancy:   vehicle.Extensions.VehicleJourney.WheelchairOccupancy,
		}

		if totalCapacity > 0 && totalOccupancy > 0 {
			locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = int((float64(totalOccupancy) / float64(totalCapacity)) * 100)
		}
	} else if vehicle.MonitoredVehicleJourney.Occupancy != "" {
		locationEvent.VehicleLocationUpdate.Occupancy = ctdf.RealtimeJourneyOccupancy{
			OccupancyAvailable: true,
			ActualValues:       false,
		}

		switch vehicle.MonitoredVehicleJourney.Occupancy {
		case "full":
			locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 100
		case "standingAvailable":
			locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 75
		case "seatsAvailable":
			locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 40
		}
	}

	return &locationEvent
}

func (s *SiriVM) SetupRealtimeQueue(queue rmq.Queue) {
	s.queue = queue
}

func (s *SiriVM) ParseFile(reader io.Reader) error {
	s.reader = reader

	return nil
}

func (s *SiriVM) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) (datasets.DataImportReport, error) {
	if !dataset.SupportedObjects.RealtimeJourneys {
		return datasets.DataImportReport{}, errors.New("This format requires realtimejourneys to be enabled")
	}

	var retrievedRecords int64
	observations := observationBatch{latest: make(map[observationRun]int)}

	d := xml.NewDecoder(s.reader)
	d.CharsetReader = charset.NewReaderLabel
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return datasets.DataImportReport{}, fmt.Errorf("decode SIRI-VM: %w", err)
		}
		element, ok := tok.(xml.StartElement)
		if !ok || element.Name.Local != "VehicleActivity" {
			continue
		}
		var activity VehicleActivity
		if err := d.DecodeElement(&activity, &element); err != nil {
			return datasets.DataImportReport{}, fmt.Errorf("decode SIRI-VM vehicle: %w", err)
		}
		retrievedRecords++
		if event := vehicleUpdate(&activity, dataset, datasource); event != nil {
			if err := observations.add(event); err != nil {
				log.Error().Err(err).Str("localID", event.LocalID).Msg("Failed to encode SIRI-VM location, skipping")
			}
		}
		if retrievedRecords%observationBatchSize == 0 {
			if err := observations.flush(s.queue); err != nil {
				return datasets.DataImportReport{}, fmt.Errorf("publish SIRI-VM observations: %w", err)
			}
		}
	}
	if err := observations.flush(s.queue); err != nil {
		return datasets.DataImportReport{}, fmt.Errorf("publish SIRI-VM observations: %w", err)
	}

	log.Info().
		Str("dataset", dataset.Identifier).
		Int64("withtrip", observations.submitted).
		Int64("withlocation", observations.submitted).
		Int64("submitted", observations.submitted).
		Int64("total", retrievedRecords).
		Int64("repeated_observations", observations.repeated).
		Int64("superseded_observations", observations.replaced).
		Msg("Submitted vehicle updates")

	// Wait for queue to empty
	checkQueueSize()

	return datasets.DataImportReport{}, nil
}

func checkQueueSize() {
	for {
		stats, _ := redis_client.QueueConnection.CollectStats([]string{"realtime-queue"})
		inQueue := stats.QueueStats["realtime-queue"].ReadyCount

		if inQueue < 40000 {
			break
		}

		log.Info().Int64("queuesize", inQueue).Msg("Queue size too long, hanging back for a bit")
		time.Sleep(time.Duration(30+rand.IntN(20)) * time.Second)
	}
}
