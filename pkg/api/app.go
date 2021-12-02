package api

import (
	"github.com/britbus/britbus/pkg/api/routes"
	"github.com/gofiber/fiber/v2"
)

func SetupServer(listen string) {
	webApp := fiber.New()

	webApp.Get("version", routes.APIVersion)

	stopsGroup := webApp.Group("/stops")
	routes.StopsRouter(stopsGroup)

	stopGroupsGroup := webApp.Group("/stopgroups")
	routes.StopGroupsRouter(stopGroupsGroup)

	webApp.Listen(listen)
}
