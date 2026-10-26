package vehicletracker

import (
	"context"
	"fmt"
	"net/http"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
)

func StartStatsServer() {
	http.Handle("/realtime-stats/queue", NewStatsHandler(redis_client.QueueConnection))
	http.Handle("/health", NewHealthHandler())

	log.Info().Msg("Stats server listening on http://localhost:3333/realtime-stats/queue")
	if err := http.ListenAndServe(":3333", nil); err != nil {
		panic(err)
	}
}

type StatsServerHandler struct {
	redisConnection rmq.Connection
}

func NewStatsHandler(connection rmq.Connection) *StatsServerHandler {
	return &StatsServerHandler{redisConnection: connection}
}
func (handler *StatsServerHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// Performance Review [Set B] [PR156] [P2] (tracker): Queue counts alone cannot distinguish slow
	// identification, cache misses, geometry, persistence, telemetry, or Ack retries.
	// Add bounded-cardinality stage latency, cache-hit/eviction, Redis-command, and
	// received-to-committed event-age metrics, plus consumer progress. Dependency
	// health can remain green while workers are stalled; profile representative feed
	// replays before tuning consumer counts, batch size, or cache limits.
	// get redis queue stats
	layout := request.FormValue("layout")
	refresh := request.FormValue("refresh")

	queues, err := handler.redisConnection.GetOpenQueues()
	if err != nil {
		panic(err)
	}

	stats, err := handler.redisConnection.CollectStats(queues)
	if err != nil {
		panic(err)
	}

	fmt.Fprint(writer, stats.GetHtml(layout, refresh))
}

type HealthHandler struct {
}

func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}
func (handler *HealthHandler) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	testRedis := redis_client.Client.ClientID(context.Background())
	if testRedis.Err() != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(writer, testRedis.Err())

		return
	}

	testMongo := database.Instance.Client.Ping(context.Background(), nil)
	if testMongo != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(writer, testMongo)

		return
	}

	writer.WriteHeader(http.StatusOK)
	fmt.Fprint(writer, "OK")
}
