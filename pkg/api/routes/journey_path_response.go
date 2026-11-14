package routes

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
)

func validateJourneyPathFormat(c *fiber.Ctx) error {
	if format := c.Query("path_format"); format != "" && format != "calls" {
		return fmt.Errorf("unsupported path_format %q", format)
	}
	return nil
}

func marshalJourneyView(c *fiber.Ctx, value any, groups []string, views sheriffViews) (any, error) {
	groups, err := sheriffGroups(c, groups, views)
	if err != nil {
		return nil, err
	}
	options := &sheriff.Options{Groups: groups}
	if c.Query("path_format") == "calls" {
		return sheriff.Marshal(options, value)
	}
	switch journey := value.(type) {
	case *ctdf.Journey:
		return marshalLegacyJourney(options, journey)
	case *ctdf.RealtimeJourney:
		return marshalLegacyRealtimeJourney(options, journey)
	default:
		return nil, fmt.Errorf("unexpected journey response %T", value)
	}
}

// Serialize legacy legs using their existing Sheriff tags. In particular,
// Sheriff emits empty slices for missing tracks, which the WebUI maps over.
func marshalLegacyJourney(options *sheriff.Options, journey *ctdf.Journey) (any, error) {
	summary := *journey
	summary.Calls, summary.RealtimeJourney = nil, nil
	data, err := sheriff.Marshal(options, summary)
	if err != nil {
		return nil, err
	}
	fields := data.(map[string]any)
	if _, included := fields["Calls"]; included {
		fields["Path"], err = sheriff.Marshal(options, journey.Legs())
		delete(fields, "Calls")
	}
	if _, included := fields["RealtimeJourney"]; err == nil && included && journey.RealtimeJourney != nil {
		fields["RealtimeJourney"], err = marshalLegacyRealtimeJourney(options, journey.RealtimeJourney)
	}
	return fields, err
}

func marshalLegacyRealtimeJourney(options *sheriff.Options, realtime *ctdf.RealtimeJourney) (any, error) {
	summary := *realtime
	summary.Journey = nil
	data, err := sheriff.Marshal(options, summary)
	if err != nil {
		return nil, err
	}
	fields := data.(map[string]any)
	if _, included := fields["Journey"]; included && realtime.Journey != nil {
		fields["Journey"], err = marshalLegacyJourney(options, realtime.Journey)
	}
	return fields, err
}

// The raw realtime endpoint keeps encoding/json's original nil slice behavior.
// The empty Calls field shadows the embedded field so only Path is emitted.
type legacyJourneyJSON struct {
	*ctdf.Journey
	Calls           []ctdf.JourneyCall `json:",omitempty"`
	Path            []*ctdf.JourneyPathItem
	RealtimeJourney *legacyRealtimeJourneyJSON
}

type legacyRealtimeJourneyJSON struct {
	*ctdf.RealtimeJourney
	Journey *legacyJourneyJSON
}

func legacyRealtimeJSON(realtime *ctdf.RealtimeJourney) *legacyRealtimeJourneyJSON {
	if realtime == nil {
		return nil
	}
	result := &legacyRealtimeJourneyJSON{RealtimeJourney: realtime}
	if journey := realtime.Journey; journey != nil {
		result.Journey = &legacyJourneyJSON{Journey: journey, Path: journey.Legs(), RealtimeJourney: legacyRealtimeJSON(journey.RealtimeJourney)}
	}
	return result
}
