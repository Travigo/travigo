package gtfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/adjust/rmq/v5"
	"github.com/kr/pretty"
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

	for _, entity := range feed.Entity {
		vehiclePosition := entity.GetVehicle()
		trip := vehiclePosition.GetTrip()
		tripID := trip.GetTripId()

		recordedAtTime := time.Unix(int64(*vehiclePosition.Timestamp), 0)

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
				Location: ctdf.Location{
					Type: "Point",
					Coordinates: []float64{
						float64(vehiclePosition.Position.GetLongitude()),
						float64(vehiclePosition.Position.GetLatitude()),
					},
				},
				Bearing:           float64(vehiclePosition.Position.GetBearing()),
				VehicleIdentifier: vehiclePosition.Vehicle.GetId(),
				Timeframe:         timeframe,
				DataSource:        datasource,
				RecordedAt:        recordedAtTime,
			}

			locationEventJson, _ := json.Marshal(locationEvent)

			r.queue.PublishBytes(locationEventJson)

		}
	}

	pretty.Println(withTripID, len(feed.Entity))
	log.Info().Int("withtrip", withTripID).Int("total", len(feed.Entity)).Msg("Submitted vehicle locations")

	return nil
}
