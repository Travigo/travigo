package elastic_client

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esutil"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/util"
)

var Client *elasticsearch.Client
var bulkIndexer esutil.BulkIndexer

func Connect(required bool) error {
	env := util.GetEnvironmentVariables()

	if env["TRAVIGO_ELASTICSEARCH_ADDRESS"] == "" && !required {
		log.Info().Msg("Skipping Elasticsearch setup")
		return nil
	} else if env["TRAVIGO_ELASTICSEARCH_ADDRESS"] == "" && required {
		log.Fatal().Msg("Elasticsearch configuration not set")
	}

	// Naughty disable TLS verify on ES endpoint
	tp := http.DefaultTransport.(*http.Transport).Clone()
	tp.TLSClientConfig.InsecureSkipVerify = true

	// Performance Review [Set D] [PR087] [P2] (indexing): This mutable backoff is shared by every
	// request/worker, but Reset and NextBackOff are not concurrency-safe. Concurrent
	// 429/5xx retries race and alter each other's delays. Derive a capped jittered
	// delay from the attempt number or use request-local state, and test concurrent
	// retry responses with the race detector rather than serial success responses.
	retryBackoff := backoff.NewExponentialBackOff()

	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{env["TRAVIGO_ELASTICSEARCH_ADDRESS"]},
		Username:  env["TRAVIGO_ELASTICSEARCH_USERNAME"],
		Password:  env["TRAVIGO_ELASTICSEARCH_PASSWORD"],
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

	// Performance Review [Set D] [PR088] [P2] (indexing): Keep and close the Info response, checking
	// its status as well as the transport error. Discarding an unread body loses
	// connection reuse and can let authentication/service errors appear as a usable
	// client. Apply a bounded startup request context to avoid hanging an indexing run.
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

	log.Info().Msgf("Elasticsearch client setup for %s", env["TRAVIGO_ELASTICSEARCH_ADDRESS"])

	return nil
}

func IndexRequest(indexName string, document io.ReadSeeker) {
	IndexRequestWithID(indexName, "", document)
}

func IndexRequestWithID(indexName string, documentID string, document io.ReadSeeker) {
	if Client == nil {
		return
	}

	// Performance Review [Set N] [PR089] [P1] (indexing): Add errors and item failures never reach
	// the caller; WaitUntilQueueEmpty also ignores Close errors and NumFailed. A
	// rejected document therefore still permits the CLI to delete legacy indexes and
	// mark the run successful. Return enqueue/flush errors, track per-index outcomes
	// and require verified success before publication/checkpointing; pass the job
	// context through queueing and flushing so unavailable ES cannot hang shutdown.
	bulkIndexer.Add(
		context.Background(),
		esutil.BulkIndexerItem{
			Index:      indexName,
			DocumentID: documentID,
			Action:     "index",
			Body:       document,
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
