package gtfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/adjust/rmq/v5"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
	"github.com/travigo/travigo/pkg/redis_client"
	"google.golang.org/protobuf/proto"
)

type Realtime struct {
	reader io.Reader
	queue  rmq.Queue

	redisCache *cache.Cache[string]
}

func (r *Realtime) SetupRealtimeQueue(queue rmq.Queue) {
	r.queue = queue

	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(90*time.Minute))

	r.redisCache = cache.New[string](redisStore)
}

func (r *Realtime) ParseFile(reader io.Reader) error {
	r.reader = reader

	return nil
}

func (r *Realtime) Import(dataset datasets.DataSet, datasource *ctdf.DataSource) error {
	if !dataset.SupportedObjects.RealtimeJourneys {
		return errors.New("This format requires realtimejourneys to be enabled")
	}

	body, err := io.ReadAll(r.reader)
	if err != nil {
		return err
	}

	feed := gtfs.FeedMessage{}
	err = proto.Unmarshal(body, &feed)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed parsing GTFS-RT protobuf")
	}

	withTripID := 0
	withLocation := 0
	withTripUpdate := 0

	for _, entity := range feed.Entity {
		vehiclePosition := entity.GetVehicle()
		tripUpdate := entity.GetTripUpdate()

		var trip *gtfs.TripDescriptor
		var recordedAtTime time.Time

		if vehiclePosition != nil {
			trip = vehiclePosition.GetTrip()
			recordedAtTime = time.Unix(int64(*vehiclePosition.Timestamp), 0)

			recordedAtDifference := time.Now().UTC().Sub(recordedAtTime)

			// Skip any records that haven't been updated in over 20 minutes
			if recordedAtDifference.Minutes() > 20 {
				continue
			}

		} else {
			recordedAtTime = time.Now()
		}
		if tripUpdate != nil {
			trip = tripUpdate.GetTrip()
		}

		tripID := trip.GetTripId()

		if entity.Alert != nil {
			pretty.Println(entity.GetAlert())
		}

		if tripID != "" {
			withTripID += 1

			var timeFrameDateTime time.Time

			if trip.StartDate == nil {
				timeFrameDateTime = time.Now()
			} else {
				timeFrameDateTime, err = time.Parse("20060102", *trip.StartDate)
				if err != nil {
					log.Error().Err(err).Msg("Failed to parse start date")
				}
			}

			timeframe := timeFrameDateTime.Format("2006-01-02")

			locationEvent := vehicletracker.VehicleLocationEvent{
				LocalID: fmt.Sprintf("%s-realtime-%s-%s", dataset.Identifier, timeframe, tripID),
				IdentifyingInformation: map[string]string{
					"TripID":        tripID,
					"RouteID":       trip.GetRouteId(),
					"LinkedDataset": dataset.LinkedDataset,
				},
				SourceType: "GTFS-RT",
				Timeframe:  timeframe,
				DataSource: datasource,
				RecordedAt: recordedAtTime,
			}

			if vehiclePosition != nil {
				if vehiclePosition.OccupancyPercentage != nil {
					locationEvent.Occupancy.OccupancyAvailable = true
					locationEvent.Occupancy.ActualValues = true

					locationEvent.Occupancy.TotalPercentageOccupancy = int(vehiclePosition.GetOccupancyPercentage())
				}

				if vehiclePosition.OccupancyStatus != nil {
					switch vehiclePosition.GetOccupancyStatus() {
					case gtfs.VehiclePosition_EMPTY:
						locationEvent.Occupancy.TotalPercentageOccupancy = 0
					case gtfs.VehiclePosition_MANY_SEATS_AVAILABLE:
						locationEvent.Occupancy.TotalPercentageOccupancy = 30
					case gtfs.VehiclePosition_FEW_SEATS_AVAILABLE:
						locationEvent.Occupancy.TotalPercentageOccupancy = 50
					case gtfs.VehiclePosition_STANDING_ROOM_ONLY:
						locationEvent.Occupancy.TotalPercentageOccupancy = 70
					case gtfs.VehiclePosition_CRUSHED_STANDING_ROOM_ONLY:
						locationEvent.Occupancy.TotalPercentageOccupancy = 80
					case gtfs.VehiclePosition_FULL:
						locationEvent.Occupancy.TotalPercentageOccupancy = 90
					case gtfs.VehiclePosition_NOT_ACCEPTING_PASSENGERS:
						locationEvent.Occupancy.TotalPercentageOccupancy = 100
					case gtfs.VehiclePosition_NO_DATA_AVAILABLE:
						locationEvent.Occupancy.TotalPercentageOccupancy = 10
					case gtfs.VehiclePosition_NOT_BOARDABLE:
						locationEvent.Occupancy.TotalPercentageOccupancy = 100
					}

					locationEvent.Occupancy.OccupancyAvailable = true
					locationEvent.Occupancy.ActualValues = false
				}

				if vehiclePosition.CongestionLevel != nil {
					pretty.Println(vehiclePosition.CongestionLevel)
				}

				if len(vehiclePosition.MultiCarriageDetails) > 0 {
					pretty.Println(vehiclePosition.MultiCarriageDetails)
				}

				locationEvent.Location = ctdf.Location{
					Type: "Point",
					Coordinates: []float64{
						float64(vehiclePosition.Position.GetLongitude()),
						float64(vehiclePosition.Position.GetLatitude()),
					},
				}

				locationEvent.Bearing = float64(vehiclePosition.Position.GetBearing())
				locationEvent.VehicleIdentifier = vehiclePosition.Vehicle.GetId()

				withLocation += 1
			}

			if tripUpdate != nil {
				for _, stopTimeUpdate := range tripUpdate.GetStopTimeUpdate() {
					locationEvent.StopUpdates = append(locationEvent.StopUpdates, vehicletracker.VehicleLocationEventStopUpdate{
						StopID:          fmt.Sprintf("%s-stop-%s", dataset.LinkedDataset, stopTimeUpdate.GetStopId()),
						ArrivalTime:     time.Unix(stopTimeUpdate.GetArrival().GetTime(), 0),
						DepartureTime:   time.Unix(stopTimeUpdate.GetDeparture().GetTime(), 0),
						ArrivalOffset:   int(stopTimeUpdate.GetArrival().GetDelay()),
						DepartureOffset: int(stopTimeUpdate.GetDeparture().GetDelay()),
					})
				}

				locationEvent.VehicleIdentifier = tripUpdate.Vehicle.GetId()

				withTripUpdate += 1
			}

			locationEventJson, _ := json.Marshal(locationEvent)

			r.queue.PublishBytes(locationEventJson)

		} else {
			if entity.Vehicle != nil && entity.Vehicle.Vehicle != nil && entity.Vehicle.Vehicle.GetId() != "" {
				vehicleID := entity.Vehicle.Vehicle.GetId()

				// Set cross dataset ID
				if vehicleID != "" {
					r.redisCache.Set(context.Background(), fmt.Sprintf("failedvehicleid/%s/%s", dataset.LinkedDataset, vehicleID), "GTFS-RT")
				}
			}
		}
	}

	log.Info().
		Int("withtrip", withTripID).
		Int("withlocation", withLocation).
		Int("withtripupdate", withTripUpdate).
		Int("total", len(feed.Entity)).
		Msg("Submitted vehicle updates")

	return nil
}
