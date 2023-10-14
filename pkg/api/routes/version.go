package routes

import (
	"runtime/debug"

	"github.com/gofiber/fiber/v2"
)

func APIVersion(c *fiber.Ctx) error {
	buildInfo, _ := debug.ReadBuildInfo()

	versionMap := fiber.Map{
		"version":   "v0.1",
		"goversion": buildInfo.GoVersion,
	}

	for _, kv := range buildInfo.Settings {
		if kv.Value == "" {
			continue
		}
		if kv.Key == "vcs.revision" {
			versionMap["commit"] = kv.Value
		}
	}

	return c.JSON(versionMap)
}
