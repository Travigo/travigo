package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/elastic_client"
)

type IdentificationRateStatsResponse struct {
	TotalRate *IdentificationRateStats

	Operators map[string]*IdentificationRateStats
}
type IdentificationRateStats struct {
	LastDayRate   float64
	LastWeekRate  float64
	LastMonthRate float64

	Rating string
}

func (i *IdentificationRateStats) CalculateRating() {
	if i.LastDayRate >= 0.95 {
		i.Rating = "PERFECT"
	} else if i.LastDayRate >= 0.75 {
		i.Rating = "EXCELLENT"
	} else if i.LastDayRate <= 0.5 && i.LastWeekRate >= 0.75 {
		i.Rating = "TEMPORARY-ISSUES"
	} else if i.LastDayRate >= 0.6 {
		i.Rating = "GOOD"
	} else {
		i.Rating = "POOR"
	}
}

type identificationRateESResponse struct {
	Error map[string]interface{}

	Aggregations struct {
		Operators struct {
			Buckets []struct {
				Key      string
				DocCount int `json:"doc_count"`
				Success  struct {
					Buckets []struct {
						Key         int
						DocCount    int    `json:"doc_count"`
						KeyAsString string `json:"key_as_string"`
					}
				}
			}
		}
	}
}

func getIdentificationRateStatsESQuery(timestampRange map[string]interface{}, operatorsList []string) *identificationRateESResponse {
	queryFilter := map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []map[string]interface{}{
				{
					"range": map[string]interface{}{
						"Timestamp": timestampRange,
					},
				},
			},
		},
	}

	if len(operatorsList) != 0 {
		var operatorQuerys []map[string]interface{}

		for _, operator := range operatorsList {
			operatorQuerys = append(operatorQuerys, map[string]interface{}{
				"match": map[string]interface{}{
					"Operator.keyword": operator,
				},
			})
		}

		operatorQueryFilterFull := map[string]interface{}{
			"bool": map[string]interface{}{
				"should": operatorQuerys,
			},
		}

		queryFilter["bool"].(map[string]interface{})["must"] = append(
			queryFilter["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
			operatorQueryFilterFull,
		)
	}

	var queryBytes bytes.Buffer
	query := map[string]interface{}{
		"query": queryFilter,
		"aggs": map[string]interface{}{
			"operators": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "Operator.keyword",
					"size":  10000,
				},
				"aggs": map[string]interface{}{
					"success": map[string]interface{}{
						"terms": map[string]interface{}{
							"field": "Success",
						},
					},
				},
			},
		},
	}

	json.NewEncoder(&queryBytes).Encode(query)
	res, err := elastic_client.Client.Search(
		elastic_client.Client.Search.WithContext(context.Background()),
		elastic_client.Client.Search.WithIndex("realtime-identify-events-*"),
		elastic_client.Client.Search.WithBody(&queryBytes),
		elastic_client.Client.Search.WithPretty(),
		elastic_client.Client.Search.WithSize(0),
	)

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to query index")
	}

	responseBytes, _ := io.ReadAll(res.Body)
	var responseStruct identificationRateESResponse
	json.Unmarshal(responseBytes, &responseStruct)

	return &responseStruct
}

func GetIdentificationRateStats(operatorsList []string) IdentificationRateStatsResponse {
	rateStats := IdentificationRateStatsResponse{
		TotalRate: &IdentificationRateStats{},
		Operators: map[string]*IdentificationRateStats{},
	}

	// TODO reduce the duplication here
	// Populate for 1 day stats
	lastDayQuery := getIdentificationRateStatsESQuery(map[string]interface{}{
		"gte": "now-1d/d",
		"lt":  "now/d",
	}, operatorsList)

	totalIdentifies := 0
	totalIdentifySuccess := 0

	for _, operator := range lastDayQuery.Aggregations.Operators.Buckets {
		if rateStats.Operators[operator.Key] == nil {
			rateStats.Operators[operator.Key] = &IdentificationRateStats{}
		}

		successCount := 0

		for _, subAggBucket := range operator.Success.Buckets {
			if subAggBucket.KeyAsString == "true" {
				successCount = subAggBucket.DocCount
			}
		}

		rateStats.Operators[operator.Key].LastDayRate = float64(successCount) / float64(operator.DocCount)

		totalIdentifies += operator.DocCount
		totalIdentifySuccess += successCount
	}
	rateStats.TotalRate.LastDayRate = float64(totalIdentifySuccess) / float64(totalIdentifies)

	// Populate for 7 day stats
	sevenDayQuery := getIdentificationRateStatsESQuery(map[string]interface{}{
		"gte": "now-7d/d",
		"lt":  "now/d",
	}, operatorsList)

	totalIdentifies = 0
	totalIdentifySuccess = 0

	for _, operator := range sevenDayQuery.Aggregations.Operators.Buckets {
		if rateStats.Operators[operator.Key] == nil {
			rateStats.Operators[operator.Key] = &IdentificationRateStats{}
		}

		successCount := 0

		for _, subAggBucket := range operator.Success.Buckets {
			if subAggBucket.KeyAsString == "true" {
				successCount = subAggBucket.DocCount
			}
		}

		rateStats.Operators[operator.Key].LastWeekRate = float64(successCount) / float64(operator.DocCount)

		totalIdentifies += operator.DocCount
		totalIdentifySuccess += successCount
	}
	rateStats.TotalRate.LastWeekRate = float64(totalIdentifySuccess) / float64(totalIdentifies)

	// Populate for 31 day stats
	lastMonthQuery := getIdentificationRateStatsESQuery(map[string]interface{}{
		"gte": "now-31d/d",
		"lt":  "now/d",
	}, operatorsList)

	totalIdentifies = 0
	totalIdentifySuccess = 0

	for _, operator := range lastMonthQuery.Aggregations.Operators.Buckets {
		if rateStats.Operators[operator.Key] == nil {
			rateStats.Operators[operator.Key] = &IdentificationRateStats{}
		}

		successCount := 0

		for _, subAggBucket := range operator.Success.Buckets {
			if subAggBucket.KeyAsString == "true" {
				successCount = subAggBucket.DocCount
			}
		}

		rateStats.Operators[operator.Key].LastMonthRate = float64(successCount) / float64(operator.DocCount)

		totalIdentifies += operator.DocCount
		totalIdentifySuccess += successCount
	}
	rateStats.TotalRate.LastMonthRate = float64(totalIdentifySuccess) / float64(totalIdentifies)

	for _, operator := range rateStats.Operators {
		operator.CalculateRating()
	}
	rateStats.TotalRate.CalculateRating()

	return rateStats
}
