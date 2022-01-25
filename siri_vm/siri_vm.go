package siri_vm

import (
	"fmt"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
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

	// responseTime, _ := time.Parse(time.RFC3339, s.ServiceDelivery.ResponseTimestamp)

	// channel, err := rabbitmq.GetChannel()
	// if err != nil {
	// 	log.Error().Err(err).Msg("Failed to create RabbitMQ Channel")
	// }
	// queue, err := channel.QueueDeclare(
	// 	"vehicle_location_events",
	// 	true,
	// 	false,
	// 	false,
	// 	false,
	// 	nil,
	// )
	// if err != nil {
	// 	log.Error().Err(err).Msg("Failed to create RabbitMQ Queue")
	// }

	for _, vehicle := range s.ServiceDelivery.VehicleMonitoringDelivery.VehicleActivity {
		vehicleJourneyRef := vehicle.MonitoredVehicleJourney.VehicleJourneyRef

		if vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef != "" {
			vehicleJourneyRef = vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef
		}

		localJourneyID := fmt.Sprintf(
			"%s:%s:%s:%s",
			fmt.Sprintf(ctdf.OperatorNOCFormat, vehicle.MonitoredVehicleJourney.OperatorRef),
			vehicle.MonitoredVehicleJourney.LineRef,
			fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.OriginRef),
			vehicleJourneyRef,
		)

		journeyRef := vehicle.MonitoredVehicleJourney.VehicleJourneyRef
		if vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef != "" {
			journeyRef = vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef
		}

		journey, err := ctdf.IdentifyJourney(map[string]string{
			"ServiceNameRef":           vehicle.MonitoredVehicleJourney.LineRef,
			"DirectionRef":             vehicle.MonitoredVehicleJourney.DirectionRef,
			"PublishedLineName":        vehicle.MonitoredVehicleJourney.PublishedLineName,
			"OperatorRef":              fmt.Sprintf(ctdf.OperatorNOCFormat, vehicle.MonitoredVehicleJourney.OperatorRef),
			"VehicleJourneyRef":        journeyRef,
			"OriginRef":                fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.OriginRef),
			"DestinationRef":           fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.DestinationRef),
			"OriginAimedDepartureTime": vehicle.MonitoredVehicleJourney.OriginAimedDepartureTime,
		})

		if err != nil {
			log.Error().Err(err).Str("localjourneyid", localJourneyID).Msgf("Could not find Journey")
			continue
		}

		pretty.Println(localJourneyID, journey.PrimaryIdentifier)

		// locationEventJSON, _ := json.Marshal(locationEvent)
		// err = channel.Publish(
		// 	"",         // exchange
		// 	queue.Name, // routing key
		// 	false,      // mandatory
		// 	false,
		// 	amqp.Publishing{
		// 		DeliveryMode: amqp.Persistent,
		// 		ContentType:  "text/plain",
		// 		Body:         []byte(locationEventJSON),
		// 	})
	}
}
