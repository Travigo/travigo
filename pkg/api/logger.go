package api

import (
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

func NewLogger() fiber.Handler {
	return func(c *fiber.Ctx) (err error) {
		startTime := time.Now()
		err = c.Next()

		msg := "HTTP Request"
		if err != nil {
			msg = err.Error()
		}

		code := c.Response().StatusCode()

		ipAddress := c.IP()

		if cloudflareConnectingIP := c.Get("CF-Connecting-IP", ""); cloudflareConnectingIP != "" {
			ipAddress = cloudflareConnectingIP
		}

		requestLogger := log.With().
			Int("status", code).
			Str("method", c.Method()).
			Str("path", c.Path()).
			Str("ip", ipAddress).
			Str("latency", time.Since(startTime).String()).
			Str("user-agent", c.Get(fiber.HeaderUserAgent)).
			Logger()

		switch {
		case code >= fiber.StatusBadRequest && code < fiber.StatusInternalServerError:
			requestLogger.Warn().Msg(msg)
		case code >= http.StatusInternalServerError:
			requestLogger.Error().Msg(msg)
		default:
			requestLogger.Info().Msg(msg)
		}

		return nil
	}
}
