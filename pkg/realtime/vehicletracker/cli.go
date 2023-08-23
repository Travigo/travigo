package vehicletracker

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kr/pretty"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/siri_vm"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker/journeyidentifier"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "vehicle-tracker",
		Usage: "Realtime engine ingests location data and tracks vehicle journeys",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run an instance of the realtime engine",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := elastic_client.Connect(false); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					ctdf.LoadSpecialDayCache()

					StartConsumers()

					StartStatsServer()

					signals := make(chan os.Signal, 1)
					signal.Notify(signals, syscall.SIGINT)
					defer signal.Stop(signals)

					<-signals // wait for signal
					go func() {
						<-signals // hard exit on second signal (in case shutdown gets stuck)
						os.Exit(1)
					}()

					<-redis_client.QueueConnection.StopAllConsuming() // wait for all Consume() calls to finish

					return nil
				},
			},
			{
				Name:  "cleaner",
				Usage: "run an the queue cleaner for the realtime queue",
				Action: func(c *cli.Context) error {
					if err := redis_client.Connect(); err != nil {
						return err
					}

					StartCleaner()

					signals := make(chan os.Signal, 1)
					signal.Notify(signals, syscall.SIGINT)
					defer signal.Stop(signals)

					<-signals // wait for signal
					go func() {
						<-signals // hard exit on second signal (in case shutdown gets stuck)
						os.Exit(1)
					}()

					<-redis_client.QueueConnection.StopAllConsuming() // wait for all Consume() calls to finish

					return nil
				},
			},
			{
				Name:  "testidentify",
				Usage: "run an instance of the realtime engine",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := elastic_client.Connect(false); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					ctdf.LoadSpecialDayCache()

					monitoredVehicleJourney := &siri_vm.MonitoredVehicleJourney{
						LineRef:           "342",
						DirectionRef:      "2",
						PublishedLineName: "73",
						FramedVehicleJourneyRef: struct {
							DataFrameRef           string
							DatedVehicleJourneyRef string
						}{},
						VehicleJourneyRef:        "480675",
						OperatorRef:              "TFLO",
						OriginRef:                "490010689KB",
						OriginName:               "Great Titchfield St / Oxford Circus Stn",
						DestinationRef:           "490015196OG",
						DestinationName:          "Holles Street",
						OriginAimedDepartureTime: "2023-08-23T13:19:00+00:00",
						VehicleLocation: struct {
							Longitude float64
							Latitude  float64
						}{Longitude: -0.141944, Latitude: 51.514797},
						Bearing:    180,
						Occupancy:  "",
						BlockRef:   "",
						VehicleRef: "LTZ1816",
					}

					vehicleJourneyRef := monitoredVehicleJourney.VehicleJourneyRef

					if monitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef != "" {
						vehicleJourneyRef = monitoredVehicleJourney.FramedVehicleJourneyRef.DatedVehicleJourneyRef
					}

					journeyIdentifier := journeyidentifier.Identifier{
						IdentifyingInformation: map[string]string{
							"ServiceNameRef":           monitoredVehicleJourney.LineRef,
							"DirectionRef":             monitoredVehicleJourney.DirectionRef,
							"PublishedLineName":        monitoredVehicleJourney.PublishedLineName,
							"OperatorRef":              "GB:NOC:TFLO",
							"VehicleJourneyRef":        vehicleJourneyRef,
							"BlockRef":                 monitoredVehicleJourney.BlockRef,
							"OriginRef":                fmt.Sprintf(ctdf.StopIDFormat, monitoredVehicleJourney.OriginRef),
							"DestinationRef":           fmt.Sprintf(ctdf.StopIDFormat, monitoredVehicleJourney.DestinationRef),
							"OriginAimedDepartureTime": monitoredVehicleJourney.OriginAimedDepartureTime,
							"FramedVehicleJourneyDate": monitoredVehicleJourney.FramedVehicleJourneyRef.DataFrameRef,
						},
					}

					journey, err := journeyIdentifier.IdentifyJourney()
					pretty.Println(journey, err)

					return nil
				},
			},
		},
	}
}
