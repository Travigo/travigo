package web_api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/travigo/travigo/pkg/http_server"
	"github.com/travigo/travigo/pkg/stats/web_api/routes"
)

func SetupServer(listen string) {
	webApp := fiber.New()
	webApp.Use(http_server.NewLogger())
	webApp.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed}))

	group := webApp.Group("/stats")

	group.Get("version", routes.APIVersion)
	routes.IdentificationRateRouter(group.Group("/identification_rate"))

	group.Get("calculated", routes.CalculatedRoute)

	webApp.Listen(listen)
}
