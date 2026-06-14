package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/api/routes"
	"github.com/travigo/travigo/pkg/http_server"
)

func SetupServer(listen string) error {
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

	routes.PlannerRouter(group.Group("/planner"))

	routes.ServiceAlertRouter(group.Group("/service_alerts"))

	routes.AccountRouter(group.Group("/account", EnsureValidToken()))

	routes.DatasourcesRouter(group.Group("/datasources"))

	return webApp.Listen(listen)
}
