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

	webApp.Listen(listen)
}
