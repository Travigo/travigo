package realtime

import (
	"fmt"
	"net/http"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/redis_client"
	"github.com/rs/zerolog/log"
)

func StartStatsServer() {
	http.Handle("/realtime-stats/overview", NewHandler(redis_client.QueueConnection))
	log.Info().Msg("Stats server listening on http://localhost:3333/realtime-stats/overview")
	if err := http.ListenAndServe(":3333", nil); err != nil {
		panic(err)
	}
}

type StatsServerHandler struct {
	connection rmq.Connection
}

func NewHandler(connection rmq.Connection) *StatsServerHandler {
	return &StatsServerHandler{connection: connection}
}

func (handler *StatsServerHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	layout := request.FormValue("layout")
	refresh := request.FormValue("refresh")

	queues, err := handler.connection.GetOpenQueues()
	if err != nil {
		panic(err)
	}

	stats, err := handler.connection.CollectStats(queues)
	if err != nil {
		panic(err)
	}

	fmt.Fprint(writer, stats.GetHtml(layout, refresh))
}
