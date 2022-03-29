package siri_vm

import (
	"context"
	"fmt"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/realtime"
	"github.com/dgraph-io/ristretto"
	"github.com/eko/gocache/v2/cache"
	"github.com/eko/gocache/v2/store"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
)

const numConsumers = 5

var identificationQueue chan *SiriVMVehicleIdentificationEvent = make(chan *SiriVMVehicleIdentificationEvent, 2000)
var identificationCache *cache.Cache

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

func StartIdentificationConsumers() {
	// Create cache
	ristrettoCache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 10000,
		MaxCost:     1 << 29,
		BufferItems: 64,
	})
	if err != nil {
		panic(err)
	}
	ristrettoStore := store.NewRistretto(ristrettoCache, &store.Options{
		Expiration: 30 * time.Minute,
	})

	identificationCache = cache.New(ristrettoStore)

	// Start the background consumers
	log.Info().Msgf("Starting identification consumers")

	for i := 0; i < numConsumers; i++ {
		go startIdentificationConsumer(i)
	}
}
func startIdentificationConsumer(id int) {
	log.Info().Msgf("Identification consumer %d started", id)
	for siriVMVehicleIdentificationEvent := range identificationQueue {
		vehicle := siriVMVehicleIdentificationEvent.VehicleActivity
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

		var journeyID string

		cachedJourneyMapping, _ := identificationCache.Get(context.Background(), localJourneyID)

		if cachedJourneyMapping == nil {
			journey, err := ctdf.IdentifyJourney(map[string]string{
				"ServiceNameRef":           vehicle.MonitoredVehicleJourney.LineRef,
				"DirectionRef":             vehicle.MonitoredVehicleJourney.DirectionRef,
				"PublishedLineName":        vehicle.MonitoredVehicleJourney.PublishedLineName,
				"OperatorRef":              fmt.Sprintf(ctdf.OperatorNOCFormat, vehicle.MonitoredVehicleJourney.OperatorRef),
				"VehicleJourneyRef":        vehicleJourneyRef,
				"OriginRef":                fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.OriginRef),
				"DestinationRef":           fmt.Sprintf(ctdf.StopIDFormat, vehicle.MonitoredVehicleJourney.DestinationRef),
				"OriginAimedDepartureTime": vehicle.MonitoredVehicleJourney.OriginAimedDepartureTime,
				"FramedVehicleJourneyDate": vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef,
			})

			if err != nil {
				// log.Error().Err(err).Str("localjourneyid", localJourneyID).Msgf("Could not find Journey")

				// Save a cache value of N/A to stop us from constantly rechecking for journeys we cant identify
				identificationCache.Set(context.Background(), localJourneyID, "N/A", nil)
				continue
			}
			journeyID = journey.PrimaryIdentifier

			identificationCache.Set(context.Background(), localJourneyID, journeyID, nil)
		} else if cachedJourneyMapping == "N/A" {
			continue
		} else {
			journeyID = cachedJourneyMapping.(string)
		}

		timeframe := vehicle.MonitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef
		if timeframe == "" {
			timeframe = time.Now().Format("2006-01-02")
		}

		// if vehicle.MonitoredVehicleJourney.PublishedLineName == "1" {
		// 	pretty.Println(journeyID)
		// }
		if journeyID == "GB:NOC:SCCM:PF0000459:27:SCCM:PF0000459:27:1::VJ400" {
			pretty.Println(vehicle.MonitoredVehicleJourney.OriginRef, vehicle.MonitoredVehicleJourney.OriginAimedDepartureTime)
		}

		vehicleLocationEvent := ctdf.VehicleLocationEvent{
			JourneyRef:       journeyID,
			Timeframe:        timeframe,
			CreationDateTime: siriVMVehicleIdentificationEvent.ResponseTime,

			DataSource: siriVMVehicleIdentificationEvent.DataSource,

			VehicleLocation: ctdf.Location{
				Type: "Point",
				Coordinates: []float64{
					vehicle.MonitoredVehicleJourney.VehicleLocation.Longitude,
					vehicle.MonitoredVehicleJourney.VehicleLocation.Latitude,
				},
			},
			VehicleBearing: vehicle.MonitoredVehicleJourney.Bearing,
		}
		realtime.AddToQueue(&vehicleLocationEvent)
	}
}

func (s *SiriVM) SubmitToProcessQueue(datasource *ctdf.DataSource) {
	datasource.OriginalFormat = "siri-vm"
	log.Info().Msgf("Submitting the %d activity records in %s to processing queue", len(s.ServiceDelivery.VehicleMonitoringDelivery.VehicleActivity), s.ServiceDelivery.VehicleMonitoringDelivery.RequestMessageRef)

	// Offset the response to the correct current timezone
	responseTimeNoOffset, _ := time.Parse(ctdf.XSDDateTimeWithFractionalFormat, s.ServiceDelivery.ResponseTimestamp)
	responseTime := responseTimeNoOffset.In(time.Now().Location())

	for _, vehicle := range s.ServiceDelivery.VehicleMonitoringDelivery.VehicleActivity {
		// TODO: filter out responses with RecordedAtTime > 30 minutes

		identificationQueue <- &SiriVMVehicleIdentificationEvent{
			VehicleActivity: vehicle,
			ResponseTime:    responseTime,
			DataSource:      datasource,
		}
	}
}
