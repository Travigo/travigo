package api

import (
	"github.com/britbus/britbus/pkg/api/routes"
	"github.com/gofiber/fiber/v2"
)

func SetupServer(listen string) {
	webApp := fiber.New()

	webApp.Get("version", routes.APIVersion)

	routes.StopsRouter(webApp.Group("/stops"))
	routes.StopGroupsRouter(webApp.Group("/stop_groups"))

	routes.OperatorsRouter(webApp.Group("/operators"))
	routes.OperatorGroupsRouter(webApp.Group("/operator_groups"))

	routes.ServicesRouter(webApp.Group("/services"))

	routes.JourneysRouter(webApp.Group("/journeys"))

	routes.RealtimeJourneysRouter(webApp.Group("/realtime_journeys"))

	webApp.Get("stats", routes.Stats)

	webApp.Listen(listen)
}
