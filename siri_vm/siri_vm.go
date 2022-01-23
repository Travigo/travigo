package siri_vm

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/rabbitmq"
	"github.com/rs/zerolog/log"
	"github.com/streadway/amqp"
)

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

func (s *SiriVM) SubmitToProcessQueue(datasource *ctdf.DataSource) {
	datasource.OriginalFormat = "siri-vm"
	log.Info().Msgf("Submitting the %d activity records in %s to processing queue", len(s.ServiceDelivery.VehicleMonitoringDelivery.VehicleActivity), s.ServiceDelivery.VehicleMonitoringDelivery.RequestMessageRef)

	responseTime, _ := time.Parse(time.RFC3339, s.ServiceDelivery.ResponseTimestamp)

	channel, err := rabbitmq.GetChannel()
	if err != nil {
		log.Error().Err(err).Msg("Failed to create RabbitMQ Channel")
	}
	queue, err := channel.QueueDeclare(
		"vehicle_location_events",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create RabbitMQ Queue")
	}

	for _, vehicle := range s.ServiceDelivery.VehicleMonitoringDelivery.VehicleActivity {
		locationEvent := ctdf.VehicleLocationEvent{
			IdentifyingInformation: map[string]string{
				"LineRef":                  vehicle.MonitoredVehicleJourney.LineRef,
				"DirectionRef":             vehicle.MonitoredVehicleJourney.DirectionRef,
				"PublishedLineName":        vehicle.MonitoredVehicleJourney.PublishedLineName,
				"OperatorRef":              fmt.Sprintf(ctdf.OperatorNOCFormat, vehicle.MonitoredVehicleJourney.OperatorRef),
				"VehicleJourneyRef":        vehicle.MonitoredVehicleJourney.VehicleJourneyRef,
				"OriginRef":                fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.OriginRef),
				"DestinationRef":           fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.DestinationRef),
				"OriginAimedDepartureTime": vehicle.MonitoredVehicleJourney.OriginAimedDepartureTime,
				"DataFrameRef":             vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef,
				"DatedVehicleJourneyRef":   vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef,
			},
			CreationDateTime: responseTime,
			DataSource:       datasource,

			VehicleLocation: ctdf.Location{
				Type:        "Point",
				Coordinates: []float64{vehicle.MonitoredVehicleJourney.VehicleLocation.Longitude, vehicle.MonitoredVehicleJourney.VehicleLocation.Latitude},
			},
			VehicleBearing: vehicle.MonitoredVehicleJourney.Bearing,
		}

		locationEventJSON, _ := json.Marshal(locationEvent)
		err = channel.Publish(
			"",         // exchange
			queue.Name, // routing key
			false,      // mandatory
			false,
			amqp.Publishing{
				DeliveryMode: amqp.Persistent,
				ContentType:  "text/plain",
				Body:         []byte(locationEventJSON),
			})
	}
}
