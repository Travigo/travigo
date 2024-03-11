package gtfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
	"google.golang.org/protobuf/proto"
)

type Realtime struct {
	reader io.Reader
	queue  rmq.Queue
}

func (r *Realtime) SetupRealtimeQueue(queue rmq.Queue) {
	r.queue = queue
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
		} else {
			recordedAtTime = time.Now()
		}
		if tripUpdate != nil {
			trip = tripUpdate.GetTrip()
		}

		tripID := trip.GetTripId()

		if tripID != "" {
			withTripID += 1

			timeFrameDateTime, _ := time.Parse("20060102", *trip.StartDate)
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