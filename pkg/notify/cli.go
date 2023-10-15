package notify

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/travigo/travigo/pkg/consumer"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "notify",
		Usage: "Provides the notification system",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run notify server",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					ctdf.LoadSpecialDayCache()

					redisConsumer := consumer.RedisConsumer{
						QueueName:       "notify-queue",
						NumberConsumers: 5,
						BatchSize:       20,
						Timeout:         2 * time.Second,
						Consumer:        NewNotifyBatchConsumer(),
					}
					redisConsumer.Setup()

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
		},
	}
}
