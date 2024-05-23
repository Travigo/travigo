package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/transforms"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func StopsRouter(router fiber.Router) {
	router.Get("/", listStops)

	router.Get("/search", searchStops)

	router.Get("/:identifier", getStop)
	router.Get("/:identifier/departures", getStopDepartures)
}

func listStops(c *fiber.Ctx) error {
	boundsQuery, err := getBoundsQuery(c)
	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	var stops []*ctdf.Stop

	stopsCollection := database.GetCollection("stops")

	bsonQuery := bson.M{"location.coordinates": boundsQuery}

	transportTypeFilter := c.Query("transport_type")
	if transportTypeFilter != "" {
		transportType := strings.Split(transportTypeFilter, ",")

		bsonQuery = bson.M{
			"$and": bson.A{
				bson.M{"transporttypes": bson.M{"$in": transportType}},
				bson.M{"location.coordinates": boundsQuery},
			},
		}
	}

	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
		bson.E{Key: "otheridentifiers", Value: 0},
		bson.E{Key: "datasource", Value: 0},
		bson.E{Key: "creationdatetime", Value: 0},
		bson.E{Key: "modificationdatetime", Value: 0},
		bson.E{Key: "associations", Value: 0},
	})

	cursor, _ := stopsCollection.Find(context.Background(), bsonQuery, opts)

	for cursor.Next(context.Background()) {
		var stop *ctdf.Stop
		err := cursor.Decode(&stop)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode Stop")
		}

		stops = append(stops, stop)
	}

	wg := sync.WaitGroup{}
	for _, stop := range stops {
		wg.Add(1)
		go func(stop *ctdf.Stop) {
			stop.Services, _ = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
				Stop: stop,
			})
			wg.Done()

			transforms.Transform(stop.Services, 1)
		}(stop)
	}
	wg.Wait()

	reducedStops, _ := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"basic"},
	}, stops)

	c.JSON(reducedStops)
	return nil
}

func getStop(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var stop *ctdf.Stop
	stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		PrimaryIdentifier: identifier,
	})

	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		stop.Services, _ = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
			Stop: stop,
		})

		transforms.Transform(stop, 3)

		reducedStop, _ := sheriff.Marshal(&sheriff.Options{
			Groups: []string{"basic", "detailed"},
		}, stop)

		return c.JSON(reducedStop)
	}
}

func getStopDepartures(c *fiber.Ctx) error {
	stopIdentifier := c.Params("identifier")
	count, err := strconv.Atoi(c.Query("count", "25"))
	startDateTimeString := c.Query("datetime")

	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "Parameter count should be an integer",
		})
	}

	var stop *ctdf.Stop
	stop, err = dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		PrimaryIdentifier: stopIdentifier,
	})

	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	var startDateTime time.Time
	if startDateTimeString == "" {
		stopTimezone, _ := time.LoadLocation(stop.Timezone)

		startDateTime = time.Now().In(stopTimezone)
	} else {
		startDateTime, err = time.Parse(time.RFC3339, startDateTimeString)

		if err != nil {
			c.SendStatus(fiber.StatusBadRequest)
			return c.JSON(fiber.Map{
				"error":    "Parameter datetime should be an RFS3339/ISO8601 datetime",
				"detailed": err,
			})
		}
	}

	var departureBoard []*ctdf.DepartureBoard

	departureBoard, err = dataaggregator.Lookup[[]*ctdf.DepartureBoard](query.DepartureBoard{
		Stop:          stop,
		Count:         count,
		StartDateTime: startDateTime,
	})

	// Sort departures by DepartureBoard time
	sort.Slice(departureBoard, func(i, j int) bool {
		return departureBoard[i].Time.Before(departureBoard[j].Time)
	})

	// Once sorted cut off any records higher than our max count
	if len(departureBoard) > count {
		departureBoard = departureBoard[:count]
	}

	departureBoardReduced, err := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"basic"},
	}, departureBoard)

	if err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": "Sherrif could not reduce departureBoard",
		})
	}

	return c.JSON(departureBoardReduced)
}

func searchStops(c *fiber.Ctx) error {
	searchTerm := c.Query("name")
	transportType := c.Query("transporttype")

	if searchTerm == "" {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "Missing `name` query parameter",
		})
	}

	if elastic_client.Client == nil {
		c.SendStatus(fiber.StatusServiceUnavailable)
		return c.JSON(fiber.Map{
			"error": "Search is currently unavailable",
		})
	}

	queryFilters := []interface{}{
		map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"match_phrase_prefix": map[string]interface{}{
							"PrimaryName.search_as_you_type": searchTerm,
						},
					},
					map[string]interface{}{
						"match_phrase_prefix": map[string]interface{}{
							"OtherIdentifiers.Crs.search_as_you_type": searchTerm,
						},
					},
					map[string]interface{}{
						"match_phrase_prefix": map[string]interface{}{
							"OtherIdentifiers.AtcoCode.search_as_you_type": searchTerm,
						},
					},
				},
			},
		},
	}

	if transportType != "" {
		queryFilters = append(queryFilters, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"match": map[string]interface{}{
							"TransportTypes": transportType,
						},
					},
				},
			},
		})
	}

	var queryBytes bytes.Buffer
	searchQuery := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": queryFilters,
			},
		},
	}

	json.NewEncoder(&queryBytes).Encode(searchQuery)
	res, err := elastic_client.Client.Search(
		elastic_client.Client.Search.WithContext(context.Background()),
		elastic_client.Client.Search.WithIndex("travigo-stops-*"),
		elastic_client.Client.Search.WithBody(&queryBytes),
		elastic_client.Client.Search.WithPretty(),
		elastic_client.Client.Search.WithSize(10),
	)

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to query index")
	}

	responseBytes, _ := io.ReadAll(res.Body)

	var responseStruct struct {
		Took     int  `json:"took"`
		TimedOut bool `json:"timed_out"`
		Hits     struct {
			MaxScore float64 `json:"max_score"`
			Total    struct {
				Value    string `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				Index  string    `json:"_index"`
				Source ctdf.Stop `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	json.Unmarshal(responseBytes, &responseStruct)

	stops := []ctdf.Stop{}
	for _, hit := range responseStruct.Hits.Hits {
		stops = append(stops, hit.Source)
	}

	return c.JSON(fiber.Map{
		"stops": stops,
	})
}
