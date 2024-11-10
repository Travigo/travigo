package gtfs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/adjust/rmq/v5"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
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
	serviceAlertCount := 0

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
			var alertType ctdf.ServiceAlertType

			switch entity.Alert.Effect {
			case gtfs.Alert_NO_SERVICE.Enum():
				alertType = ctdf.ServiceAlertTypeServiceSuspended
			case gtfs.Alert_REDUCED_SERVICE.Enum():
				alertType = ctdf.ServiceAlertTypeServicePartSuspended
			case gtfs.Alert_SIGNIFICANT_DELAYS.Enum():
				alertType = ctdf.ServiceAlertTypeSevereDelays
			case gtfs.Alert_DETOUR.Enum():
				alertType = ctdf.ServiceAlertTypeWarning // TODO new type?
			case gtfs.Alert_ADDITIONAL_SERVICE.Enum():
				alertType = ctdf.ServiceAlertTypeInformation
			case gtfs.Alert_MODIFIED_SERVICE.Enum():
				alertType = ctdf.ServiceAlertTypeWarning
			case gtfs.Alert_OTHER_EFFECT.Enum():
				alertType = ctdf.ServiceAlertTypeInformation
			case gtfs.Alert_UNKNOWN_EFFECT.Enum():
				alertType = ctdf.ServiceAlertTypeInformation
			case gtfs.Alert_STOP_MOVED.Enum():
				alertType = ctdf.ServiceAlertTypeWarning
			case gtfs.Alert_NO_EFFECT.Enum():
				alertType = ctdf.ServiceAlertTypeInformation
			case gtfs.Alert_ACCESSIBILITY_ISSUE.Enum():
				alertType = ctdf.ServiceAlertTypeInformation
			default:
				alertType = ctdf.ServiceAlertTypeInformation
			}

			// Create one per active period & informed entity
			var identifyingInformation []map[string]string

			for _, informedEntity := range entity.Alert.InformedEntity {
				tripID := informedEntity.GetTrip().GetTripId()
				routeID := informedEntity.GetRouteId()
				stopID := informedEntity.GetStopId()
				agencyID := informedEntity.GetAgencyId()

				identifyingInformation = append(identifyingInformation, map[string]string{
					"TripID":        tripID,
					"RouteID":       routeID,
					"StopID":        stopID,
					"AgencyID":      agencyID,
					"LinkedDataset": dataset.LinkedDataset,
				})
			}

			for _, activePeriod := range entity.Alert.ActivePeriod {
				validFromTimestamp := activePeriod.GetStart()
				validToTimestamp := activePeriod.GetEnd()

				validFrom := time.Unix(int64(validFromTimestamp), 0)
				validTo := time.Unix(int64(validToTimestamp), 0)

				title := *entity.Alert.HeaderText.GetTranslation()[0].Text // TODO assume we only use the 1 translation
				description := *entity.Alert.DescriptionText.GetTranslation()[0].Text

				hash := sha256.New()
				hash.Write([]byte(alertType))
				hash.Write([]byte(title))
				hash.Write([]byte(description))
				localIDhash := fmt.Sprintf("%x", hash.Sum(nil))

				updateEvent := vehicletracker.VehicleUpdateEvent{
					MessageType: vehicletracker.VehicleUpdateEventTypeServiceAlert,
					LocalID:     fmt.Sprintf("%s-servicealert-%d-%d-%s", dataset.Identifier, validFromTimestamp, validToTimestamp, localIDhash),

					ServiceAlertUpdate: &vehicletracker.ServiceAlertUpdate{
						Type:        alertType,
						Title:       title,
						Description: description,
						ValidFrom:   validFrom,
						ValidUntil:  validTo,

						IdentifyingInformation: identifyingInformation,
					},

					SourceType: "GTFS-RT",
					DataSource: datasource,
					RecordedAt: recordedAtTime,
				}

				updateEventJson, _ := json.Marshal(updateEvent)
				r.queue.PublishBytes(updateEventJson)

				serviceAlertCount += 1
			}
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

			locationEvent := vehicletracker.VehicleUpdateEvent{
				MessageType: vehicletracker.VehicleUpdateEventTypeTrip,
				LocalID:     fmt.Sprintf("%s-realtime-%s-%s", dataset.Identifier, timeframe, tripID),
				SourceType:  "GTFS-RT",
				VehicleLocationUpdate: &vehicletracker.VehicleLocationUpdate{
					Timeframe: timeframe,

					IdentifyingInformation: map[string]string{
						"TripID":        tripID,
						"RouteID":       trip.GetRouteId(),
						"LinkedDataset": dataset.LinkedDataset,
					},
				},
				DataSource: datasource,
				RecordedAt: recordedAtTime,
			}

			if vehiclePosition != nil {
				if vehiclePosition.OccupancyPercentage != nil {
					locationEvent.VehicleLocationUpdate.Occupancy.OccupancyAvailable = true
					locationEvent.VehicleLocationUpdate.Occupancy.ActualValues = true

					locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = int(vehiclePosition.GetOccupancyPercentage())
				}

				if vehiclePosition.OccupancyStatus != nil {
					switch vehiclePosition.GetOccupancyStatus() {
					case gtfs.VehiclePosition_EMPTY:
						locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 0
					case gtfs.VehiclePosition_MANY_SEATS_AVAILABLE:
						locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 30
					case gtfs.VehiclePosition_FEW_SEATS_AVAILABLE:
						locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 50
					case gtfs.VehiclePosition_STANDING_ROOM_ONLY:
						locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 70
					case gtfs.VehiclePosition_CRUSHED_STANDING_ROOM_ONLY:
						locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 80
					case gtfs.VehiclePosition_FULL:
						locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 90
					case gtfs.VehiclePosition_NOT_ACCEPTING_PASSENGERS:
						locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 100
					case gtfs.VehiclePosition_NO_DATA_AVAILABLE:
						locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 10
					case gtfs.VehiclePosition_NOT_BOARDABLE:
						locationEvent.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy = 100
					}

					locationEvent.VehicleLocationUpdate.Occupancy.OccupancyAvailable = true
					locationEvent.VehicleLocationUpdate.Occupancy.ActualValues = false
				}

				if vehiclePosition.CongestionLevel != nil {
					// pretty.Println(vehiclePosition.CongestionLevel)
					collection := database.GetCollection("datadump")
					collection.InsertOne(context.Background(), bson.M{
						"type":             "gtfsrt-congestionlevel",
						"creationdatetime": time.Now(),
						"document":         vehiclePosition,
					})
				}

				if len(vehiclePosition.MultiCarriageDetails) > 0 {
					// pretty.Println(vehiclePosition.MultiCarriageDetails)
					collection := database.GetCollection("datadump")
					collection.InsertOne(context.Background(), bson.M{
						"type":             "gtfsrt-multicarriagedetails",
						"creationdatetime": time.Now(),
						"document":         vehiclePosition,
					})
				}

				locationEvent.VehicleLocationUpdate.Location = ctdf.Location{
					Type: "Point",
					Coordinates: []float64{
						float64(vehiclePosition.Position.GetLongitude()),
						float64(vehiclePosition.Position.GetLatitude()),
					},
				}

				locationEvent.VehicleLocationUpdate.Bearing = float64(vehiclePosition.Position.GetBearing())
				locationEvent.VehicleLocationUpdate.VehicleIdentifier = vehiclePosition.Vehicle.GetId()

				withLocation += 1
			}

			if tripUpdate != nil {
				for _, stopTimeUpdate := range tripUpdate.GetStopTimeUpdate() {
					locationEvent.VehicleLocationUpdate.StopUpdates = append(locationEvent.VehicleLocationUpdate.StopUpdates, vehicletracker.VehicleLocationEventStopUpdate{
						StopID:          fmt.Sprintf("%s-stop-%s", dataset.LinkedDataset, stopTimeUpdate.GetStopId()),
						ArrivalTime:     time.Unix(stopTimeUpdate.GetArrival().GetTime(), 0),
						DepartureTime:   time.Unix(stopTimeUpdate.GetDeparture().GetTime(), 0),
						ArrivalOffset:   int(stopTimeUpdate.GetArrival().GetDelay()),
						DepartureOffset: int(stopTimeUpdate.GetDeparture().GetDelay()),
					})
				}

				locationEvent.VehicleLocationUpdate.VehicleIdentifier = tripUpdate.Vehicle.GetId()

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
		Int("servicealert", serviceAlertCount).
		Int("total", len(feed.Entity)).
		Msg("Submitted vehicle updates")

	checkQueueSize()

	return nil
}

func checkQueueSize() {
	stats, _ := redis_client.QueueConnection.CollectStats([]string{"realtime-queue"})
	inQueue := stats.QueueStats["realtime-queue"].ReadyCount

	if inQueue >= 40000 {
		log.Info().Int64("queuesize", inQueue).Msg("Queue size too long, hanging back for a bit")
		time.Sleep(time.Duration(30+rand.IntN(20)) * time.Minute)

		checkQueueSize()
	}
}
