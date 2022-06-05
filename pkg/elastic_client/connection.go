package elastic_client

import (
	"net/http"

	"github.com/britbus/britbus/pkg/util"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/rs/zerolog/log"
)

var Client *elasticsearch.Client

func Connect(required bool) error {
	setupBatchIndexer()

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
