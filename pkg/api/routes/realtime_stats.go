package routes

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/realtime/runtimestats"
)

func RealtimeStats(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	return c.JSON(runtimestats.GetSnapshot(ctx))
}
