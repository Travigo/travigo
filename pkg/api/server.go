package api

import (
	"github.com/britbus/britbus/pkg/api/routes"
	"github.com/britbus/britbus/pkg/api/stats"
	"github.com/britbus/britbus/pkg/http_server"
	"github.com/gofiber/fiber/v2"
)

func SetupServer(listen string) {
	go stats.UpdateRecordsStats()

	webApp := fiber.New()
	webApp.Use(http_server.NewLogger())

	group := webApp.Group("/core")

	group.Get("version", routes.APIVersion)

	routes.StopsRouter(group.Group("/stops"))
	routes.StopGroupsRouter(group.Group("/stop_groups"))

	routes.OperatorsRouter(group.Group("/operators"))
	routes.OperatorGroupsRouter(group.Group("/operator_groups"))

	routes.ServicesRouter(group.Group("/services"))

	routes.JourneysRouter(group.Group("/journeys"))

	routes.RealtimeJourneysRouter(group.Group("/realtime_journeys"))

	group.Get("stats", routes.Stats)

	webApp.Listen(listen)
}
