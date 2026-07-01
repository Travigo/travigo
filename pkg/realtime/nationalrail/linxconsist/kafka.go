package linxconsist

import (
	"context"
	"crypto/tls"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
)

const DefaultBootstrapServer = "pkc-z3p1v0.europe-west2.gcp.confluent.cloud:9092"

type KafkaClient struct {
	Brokers  []string
	Topic    string
	GroupID  string
	Username string
	Password string
}

func (k *KafkaClient) Run(ctx context.Context) error {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: k.Brokers,
		Topic:   k.Topic,
		GroupID: k.GroupID,
		Dialer: &kafka.Dialer{
			Timeout:       10 * time.Second,
			DualStack:     true,
			SASLMechanism: plain.Mechanism{Username: k.Username, Password: k.Password},
			TLS:           &tls.Config{MinVersion: tls.VersionTLS12},
		},
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	defer reader.Close()

	log.Info().
		Strs("brokers", k.Brokers).
		Str("topic", k.Topic).
		Str("group", k.GroupID).
		Msg("Started LINX passenger train consist Kafka consumer")

	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Error().Err(err).Msg("Failed to fetch LINX consist Kafka message")
			continue
		}

		toc := kafkaHeader(message.Headers, "toc")
		if err := ProcessMessage(ctx, message.Value, toc); err != nil {
			log.Error().
				Err(err).
				Str("topic", message.Topic).
				Int("partition", message.Partition).
				Int64("offset", message.Offset).
				Msg("Failed to process LINX consist Kafka message")
		}

		if err := reader.CommitMessages(ctx, message); err != nil {
			log.Error().Err(err).Msg("Failed to commit LINX consist Kafka message")
		}
	}
}

func kafkaHeader(headers []kafka.Header, key string) string {
	for _, header := range headers {
		if strings.EqualFold(header.Key, key) {
			return string(header.Value)
		}
	}
	return ""
}
