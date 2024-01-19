package indexer

import (
	"context"
	"encoding/json"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/elastic_client"
	"io"
)

func deleteOldIndexes(indexWildcard string, indexName string) {
	catReq := esapi.CatIndicesRequest{
		Index:  []string{indexWildcard},
		Format: "json",
	}

	resp, err := catReq.Do(context.Background(), elastic_client.Client)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to list index")
	}

	var indexes []struct {
		Index string `json:"index"`
	}

	responseBytes, _ := io.ReadAll(resp.Body)
	json.Unmarshal(responseBytes, &indexes)

	for _, index := range indexes {
		if index.Index != indexName {
			deleteReq := esapi.IndicesDeleteRequest{
				Index: []string{index.Index},
			}

			deleteReq.Do(context.Background(), elastic_client.Client)

			log.Info().Str("index", index.Index).Msg("Delete old index")
		}
	}
}
