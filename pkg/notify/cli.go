package notify

import (
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
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

					pushManager := &PushManager{}
					err := pushManager.Setup()
					if err != nil {
						return err
					}

					redisConsumer := consumer.RedisConsumer{
						QueueName:       "notify-queue",
						NumberConsumers: 5,
						BatchSize:       20,
						Timeout:         1 * time.Second,
						Consumer:        NewNotifyBatchConsumer(pushManager),
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

			{
				Name:  "test-notification",
				Usage: "generate a test notification",
				Action: func(c *cli.Context) error {
					if err := redis_client.Connect(); err != nil {
						return err
					}

					notifyQueue, err := redis_client.QueueConnection.OpenQueue("notify-queue")
					if err != nil {
						log.Fatal().Err(err).Msg("Failed to start notify queue")
					}

					notification := ctdf.Notification{
						TargetUser: "auth0|651c491ffcd735ea268f65fb",
						Type:       ctdf.NotificationTypePush,
						Title:      "Line Suspended",
						Message:    "Northern Line has been suspended due to a fault on the line",
					}

					notificationBytes, _ := json.Marshal(notification)

					notifyQueue.PublishBytes(notificationBytes)

					return nil
				},
			},
		},
	}
}
