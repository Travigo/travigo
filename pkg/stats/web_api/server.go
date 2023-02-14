package web_api

import (
	"github.com/britbus/britbus/pkg/http_server"
	"github.com/britbus/britbus/pkg/stats/routes"
	"github.com/gofiber/fiber/v2"
)

func SetupServer(listen string) {
	webApp := fiber.New()
	webApp.Use(http_server.NewLogger())

	group := webApp.Group("/stats")

	group.Get("version", routes.APIVersion)
	routes.IdentificationRateRouter(group.Group("/identification_rate"))

	webApp.Listen(listen)
}
