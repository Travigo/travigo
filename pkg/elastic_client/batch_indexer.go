package elastic_client

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/MasterOfBinary/gobatch/batch"
	"github.com/MasterOfBinary/gobatch/source"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/rs/zerolog/log"
)

var indexRequestChannel chan interface{}

type indexProcessor struct{}

func (i indexProcessor) Process(ctx context.Context, ps *batch.PipelineStage) {
	// Process needs to close ps after it's done
	defer ps.Close()

	// TODO: this should actually do a batch request instead of lots of individual requests
	for item := range ps.Input {
		req := item.Get().(*esapi.IndexRequest)
		// Perform the request with the client.
		res, err := req.Do(context.Background(), Client)
		if err != nil {
			log.Error().Err(err).Msg("Error getting response")
		}
		defer res.Body.Close()

		if res.IsError() {
			log.Error().Msgf("[%s] Error indexing document", res.Status())
			io.Copy(os.Stdout, res.Body)
		}
	}
}

func setupBatchIndexer() {
	// Create a batch processor that processes items 5 at a time
	config := batch.NewConstantConfig(&batch.ConfigValues{
		MaxItems: 50,
		MinTime:  1 * time.Second,
		MaxTime:  1 * time.Second,
	})
	b := batch.New(config)
	p := &indexProcessor{}

	// Channel is a Source that reads from a channel until it's closed
	indexRequestChannel = make(chan interface{}, 10000)
	s := source.Channel{
		Input: indexRequestChannel,
	}

	// Go runs in the background while the main goroutine processes errors
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b.Go(ctx, &s, p)
}

func IndexRequest(req *esapi.IndexRequest) {
	if Client == nil {
		return
	}

	indexRequestChannel <- req
}
