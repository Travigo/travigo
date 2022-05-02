package elastic_client

import (
	"context"
	"net/http"

	"github.com/britbus/britbus/pkg/util"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
)

var Client *elasticsearch.Client

func Connect() error {
	pretty.Println("WTF")
	env := util.GetEnvironmentVariables()

	if env["BRITBUS_ELASTICSEARCH_ADDRESS"] == "" {
		log.Info().Msg("Skipping Elasticsearch setup")
		return nil
	}

	// Naughty disable TLS verify on ES endpoint
	// TODO: fix this at some point
	tp := http.DefaultTransport.(*http.Transport).Clone()
	tp.TLSClientConfig.InsecureSkipVerify = true

	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{env["BRITBUS_ELASTICSEARCH_ADDRESS"]},
		Username:  env["BRITBUS_ELASTICSEARCH_USERNAME"],
		Password:  env["BRITBUS_ELASTICSEARCH_PASSWORD"],
		Transport: tp,
	})
	if err != nil {
		return err
	}

	_, err = es.Info()
	if err != nil {
		return err
	}

	Client = es

	log.Info().Msgf("Elasticsearch client setup for %s", env["BRITBUS_ELASTICSEARCH_ADDRESS"])

	return nil
}

func IndexRequest(req esapi.IndexRequest) {
	if Client == nil {
		return
	}

	// Perform the request with the client.
	res, err := req.Do(context.Background(), Client)
	if err != nil {
		log.Error().Err(err).Msg("Error getting response")
	}
	defer res.Body.Close()

	if res.IsError() {
		log.Error().Msgf("[%s] Error indexing document", res.Status())
	}
}
