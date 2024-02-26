package gtfs

import (
	"io"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/adjust/rmq/v5"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"google.golang.org/protobuf/proto"
)

func ParseRealtime(reader io.Reader, queue rmq.Queue, datasource *ctdf.DataSource) error {
	body, err := io.ReadAll(reader)
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
			pretty.Println(tripID, recordedAtTime, vehiclePosition)
		}
	}

	pretty.Println(withTripID, len(feed.Entity))

	return nil
}
