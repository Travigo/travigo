package realtime

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/redis_client"
	"github.com/rs/zerolog/log"
)

func StartStatsServer() {
	http.Handle("/realtime-stats/queue", NewStatsHandler(redis_client.QueueConnection))
	http.Handle("/realtime-stats/errors", &ErrorStatsServerHandler{})
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

type ErrorStatsServerHandler struct{}

func (handler *ErrorStatsServerHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// get error type stats
	fmt.Fprint(writer, "<html><body><h2>Error types</23>")

	errorTypesScan := redis_client.Client.Keys(context.Background(), "ERRORTRACKTYPE_*")
	errorTypesResults, _ := errorTypesScan.Result()

	fmt.Fprint(writer, "<table>")
	for _, typeKey := range errorTypesResults {
		keyVal := redis_client.Client.Get(context.TODO(), typeKey).Val()
		fmt.Fprintf(writer, "<tr><td>%s</td><td>%s</td></tr>", typeKey, keyVal)
	}
	fmt.Fprint(writer, "</table>")

	// Get top failed operators
	fmt.Fprint(writer, "<html><body><h2>Failed operators</23>")

	operatorsScan := redis_client.Client.Keys(context.Background(), "ERRORTRACKOPERATOR_*")
	operatorsScanResults, _ := operatorsScan.Result()

	fmt.Fprint(writer, "<table>")
	for _, operatorKey := range operatorsScanResults {
		keyVal := redis_client.Client.Get(context.TODO(), operatorKey).Val()
		operator := strings.Split(operatorKey, "_")[1]
		fmt.Fprintf(writer, "<tr><td>%s</td><td>%s</td></tr>", operator, keyVal)
	}
	fmt.Fprint(writer, "</table>")

	fmt.Fprint(writer, "</body></html>")
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
