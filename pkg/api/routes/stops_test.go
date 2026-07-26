package routes

import (
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
)

func TestResolveStopDisplayNameUsesServiceOverride(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "stop-primary",
		OtherIdentifiers:  []string{"stop-other"},
		PrimaryName:       "Original",
	}

	displayName, overrideApplied := resolveStopDisplayName(stop, map[string]string{
		"stop-other": "Overridden",
	})

	if displayName != "Overridden" {
		t.Fatalf("expected overridden display name, got %q", displayName)
	}
	if !overrideApplied {
		t.Fatal("expected override to be reported as applied")
	}
}

func TestResolveStopDisplayNameFallsBackToPrimaryName(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "stop-primary",
		PrimaryName:       "Original",
	}

	displayName, overrideApplied := resolveStopDisplayName(stop, nil)

	if displayName != "Original" {
		t.Fatalf("expected primary display name, got %q", displayName)
	}
	if overrideApplied {
		t.Fatal("did not expect override to be reported as applied")
	}
}

func TestStopSearchTransportTypesSupportsMultiSelect(t *testing.T) {
	app := fiber.New()
	var transportTypes []string
	app.Get("/", func(c *fiber.Ctx) error {
		transportTypes = stopSearchTransportTypes(c)
		return c.SendStatus(fiber.StatusNoContent)
	})

	query := url.Values{}
	query.Add("transporttype", "Bus,Rail")
	query.Add("transporttype", "Tram")
	query.Add("transport_type", " Rail,Metro ")
	request, err := app.Test(httptest.NewRequest("GET", "/?"+query.Encode(), nil))
	if err != nil {
		t.Fatalf("request failed: %s", err)
	}
	request.Body.Close()

	expected := []string{"Bus", "Rail", "Tram", "Metro"}
	if !reflect.DeepEqual(transportTypes, expected) {
		t.Fatalf("transport types = %#v, want %#v", transportTypes, expected)
	}
}
