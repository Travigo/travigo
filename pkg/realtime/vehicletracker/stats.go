package vehicletracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/runtimestats"
	"github.com/travigo/travigo/pkg/redis_client"
)

func StartStatsServer() {
	http.Handle("/realtime-stats/queue", NewStatsHandler(redis_client.QueueConnection))
	http.Handle("/realtime-stats/runtime", NewRuntimeStatsHandler())
	http.Handle("/health", NewHealthHandler())

	log.Info().Msg("Stats server listening on http://localhost:3333/realtime-stats")
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

type RuntimeStatsHandler struct{}

func NewRuntimeStatsHandler() *RuntimeStatsHandler {
	return &RuntimeStatsHandler{}
}

func (handler *RuntimeStatsHandler) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(runtimestats.GetSnapshot(ctx)); err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(writer, err)
	}
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
