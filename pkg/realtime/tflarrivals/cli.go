package tflarrivals

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "tfl-arrivals",
		Usage: "Track vehicle progress through the TfL arrivals API",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run an instance an instance of vehicle tracker",
				Action: func(c *cli.Context) error {
					if err := redis_client.Connect(); err != nil {
						return err
					}

					trackerManager := TrackerManager{
						Modes: []*TfLMode{
							{
								ModeID:                "bus",
								TransportType:         ctdf.TransportTypeBus,
								TrackArrivals:         false,
								TrackDisruptions:      true,
								DisruptionRefreshRate: 2 * time.Minute,
							},
							{
								ModeID:                "dlr",
								TransportType:         ctdf.TransportTypeRail,
								TrackArrivals:         true,
								TrackDisruptions:      true,
								ArrivalRefreshRate:    15 * time.Second,
								DisruptionRefreshRate: 2 * time.Minute,
							},
							{
								ModeID:                "river-bus",
								TransportType:         ctdf.TransportTypeFerry,
								TrackArrivals:         true,
								TrackDisruptions:      true,
								ArrivalRefreshRate:    45 * time.Second,
								DisruptionRefreshRate: 2 * time.Minute,
							},
							{
								ModeID:                "tram",
								TransportType:         ctdf.TransportTypeTram,
								TrackArrivals:         true,
								TrackDisruptions:      true,
								ArrivalRefreshRate:    15 * time.Second,
								DisruptionRefreshRate: 2 * time.Minute,
							},
							{
								ModeID:                "tube",
								TransportType:         ctdf.TransportTypeMetro,
								TrackArrivals:         true,
								TrackDisruptions:      true,
								ArrivalRefreshRate:    15 * time.Second,
								DisruptionRefreshRate: 2 * time.Minute,
							},

							// { // HAS ZERO LINES
							// 	ModeID:                "river-tour",
							// 	TransportType:         ctdf.TransportTypeFerry,
							// 	TrackArrivals:         true,
							// 	TrackDisruptions:      true,
							// 	ArrivalRefreshRate:    45 * time.Second,
							// 	DisruptionRefreshRate: 5 * time.Minute,
							// },
							// { // NO CTDF SERVICE
							// 	ModeID:                "cable-car",
							// 	TransportType:         ctdf.TransportTypeCableCar,
							// 	TrackArrivals:         true,
							// 	TrackDisruptions:      true,
							// 	ArrivalRefreshRate:    45 * time.Second,
							// 	DisruptionRefreshRate: 5 * time.Minute,
							// },
							// { // NO CTDF SERVICE
							// 	ModeID:                "overground",
							// 	TransportType:         ctdf.TransportTypeRail,
							// 	TrackArrivals:         false,
							// 	TrackDisruptions:      true,
							// 	DisruptionRefreshRate: 5 * time.Minute,
							// },
						},
					}
					trackerManager.Run()

					signals := make(chan os.Signal, 1)
					signal.Notify(signals, syscall.SIGINT)
					defer signal.Stop(signals)

					<-signals // wait for signal
					go func() {
						<-signals // hard exit on second signal (in case shutdown gets stuck)
						os.Exit(1)
					}()

					return nil
				},
			},
		},
	}
}
