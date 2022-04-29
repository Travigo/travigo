package realtime

import (
	"context"
	"fmt"
	"net/http"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/redis_client"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/mongo"
)

func StartStatsServer() {
	http.Handle("/realtime-stats/overview", NewStatsHandler(redis_client.QueueConnection))
	http.Handle("/health", NewHealthHandler())

	log.Info().Msg("Stats server listening on http://localhost:3333/realtime-stats/overview")
	if err := http.ListenAndServe(":3333", nil); err != nil {
		panic(err)
	}
}

type StatsServerHandler struct {
	redisConnection rmq.Connection
	mongoConnection *mongo.Client
}

func NewStatsHandler(connection rmq.Connection) *StatsServerHandler {
	return &StatsServerHandler{redisConnection: connection}
}
func (handler *StatsServerHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
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
func (handler *HealthHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	testRedis := redis_client.Client.ClientID(context.TODO())
	if testRedis.Err() != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(writer, testRedis.Err())

		return
	}

	testMongo := database.Instance.Client.Ping(context.TODO(), nil)
	if testMongo != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(writer, testMongo)

		return
	}

	writer.WriteHeader(http.StatusOK)
	fmt.Fprint(writer, "OK")
}
