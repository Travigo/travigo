package routes

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
)

type sheriffViews map[string][]string

func validateSheriffView(c *fiber.Ctx, views sheriffViews) error {
	view := c.Query("view")
	if view == "" {
		return nil
	}
	if _, ok := views[view]; !ok {
		return fmt.Errorf("unsupported view %q", view)
	}
	return nil
}

func marshalWithSheriffView(c *fiber.Ctx, value interface{}, defaultGroups []string, views sheriffViews) (interface{}, error) {
	groups := defaultGroups
	if view := c.Query("view"); view != "" {
		if err := validateSheriffView(c, views); err != nil {
			return nil, err
		}
		groups = views[view]
	}

	return sheriff.Marshal(&sheriff.Options{Groups: groups}, value)
}

func sheriffViewError(c *fiber.Ctx, err error) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
}
