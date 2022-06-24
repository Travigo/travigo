package elastic_client

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/britbus/britbus/pkg/util"
	"github.com/cenkalti/backoff/v4"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esutil"
	"github.com/rs/zerolog/log"
)

var Client *elasticsearch.Client
var bulkIndexer esutil.BulkIndexer

func Connect(required bool) error {
	env := util.GetEnvironmentVariables()

	if env["BRITBUS_ELASTICSEARCH_ADDRESS"] == "" && !required {
		log.Info().Msg("Skipping Elasticsearch setup")
		return nil
	} else if env["BRITBUS_ELASTICSEARCH_ADDRESS"] == "" && required {
		log.Fatal().Msg("Elasticsearch configuration not set")
	}

	// Naughty disable TLS verify on ES endpoint
	// TODO: fix this at some point
	tp := http.DefaultTransport.(*http.Transport).Clone()
	tp.TLSClientConfig.InsecureSkipVerify = true

	retryBackoff := backoff.NewExponentialBackOff()

	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{env["BRITBUS_ELASTICSEARCH_ADDRESS"]},
		Username:  env["BRITBUS_ELASTICSEARCH_USERNAME"],
		Password:  env["BRITBUS_ELASTICSEARCH_PASSWORD"],
		Transport: tp,

		RetryOnStatus: []int{502, 503, 504, 429},

		RetryBackoff: func(i int) time.Duration {
			if i == 1 {
				retryBackoff.Reset()
			}
			return retryBackoff.NextBackOff()
		},
		MaxRetries: 5,
	})
	if err != nil {
		return err
	}

	_, err = es.Info()
	if err != nil {
		return err
	}

	Client = es

	bulkIndexer, err = esutil.NewBulkIndexer(esutil.BulkIndexerConfig{
		Client:        es,               // The Elasticsearch client
		FlushInterval: 15 * time.Second, // The periodic flush interval
	})
	if err != nil {
		return err
	}

	log.Info().Msgf("Elasticsearch client setup for %s", env["BRITBUS_ELASTICSEARCH_ADDRESS"])

	return nil
}

func IndexRequest(indexName string, document io.ReadSeeker) {
	if Client == nil {
		return
	}

	bulkIndexer.Add(
		context.Background(),
		esutil.BulkIndexerItem{
			Index:  indexName,
			Action: "index",
			Body:   document,
			OnFailure: func(ctx context.Context, item esutil.BulkIndexerItem, res esutil.BulkIndexerResponseItem, err error) {
				if err != nil {
					log.Error().Err(err).Str("indexName", indexName).Msg("Failed to index document")
				} else {
					log.Error().Str("type", res.Error.Type).Str("reason", res.Error.Reason).Msg("Failed to index document")
				}
			},
		},
	)
}

func WaitUntilQueueEmpty() {
	bulkIndexer.Close(context.Background())
}
