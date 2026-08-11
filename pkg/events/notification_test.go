package events

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
)

type notificationTestDataSource struct{}

func (notificationTestDataSource) GetName() string {
	return "notification test data source"
}

func (notificationTestDataSource) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(ctdf.Stop{}),
		reflect.TypeOf(ctdf.Service{}),
		reflect.TypeOf(ctdf.Journey{}),
	}
}

func (notificationTestDataSource) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.Stop:
		return &ctdf.Stop{PrimaryName: "Cambridge"}, nil
	case query.Service:
		return &ctdf.Service{ServiceName: "Fast Rail"}, nil
	case query.Journey:
		return &ctdf.Journey{OriginDisplay: "Cambridge", DestinationDisplay: "London"}, nil
	default:
		return nil, nil
	}
}

func useNotificationTestDataSource(t *testing.T) {
	t.Helper()
	previous := dataaggregator.GlobalAggregator
	dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}
	dataaggregator.GlobalAggregator.RegisterSource(notificationTestDataSource{})
	t.Cleanup(func() {
		dataaggregator.GlobalAggregator = previous
	})
}

func TestGetNotificationDataServiceAlertUsesConfiguredIdentifier(t *testing.T) {
	useNotificationTestDataSource(t)

	data := GetNotificationData(&ctdf.Event{
		Type: ctdf.EventTypeServiceAlertCreated,
		Body: map[string]interface{}{
			"AlertType":          "Delays",
			"Title":              "",
			"Text":               "Expect delays.",
			"MatchedIdentifiers": []interface{}{"stop-other", "service-1", "journey-other"},
		},
	}, ctdf.UserNotificationSubscription{
		Values: ctdf.UserNotificationSubscriptionValues{ServiceRef: "service-1"},
	})

	if data.Message != "Fast Rail\nExpect delays." {
		t.Fatalf("notification message = %q, want configured identifier only", data.Message)
	}
}

func TestGetNotificationDataServiceAlertNormalisesConfiguredJourneyIdentifier(t *testing.T) {
	useNotificationTestDataSource(t)

	data := GetNotificationData(&ctdf.Event{
		Type: ctdf.EventTypeServiceAlertCreated,
		Body: map[string]interface{}{
			"AlertType":          "JourneyDelayed",
			"Title":              "",
			"Text":               "The journey is delayed.",
			"MatchedIdentifiers": []interface{}{"stop-other", "DAYINSTANCEOF:20260811:journey-1", "service-other"},
		},
	}, ctdf.UserNotificationSubscription{
		Values: ctdf.UserNotificationSubscriptionValues{JourneyRef: "journey-1"},
	})

	if data.Message != "Cambridge → London\nThe journey is delayed." {
		t.Fatalf("notification message = %q, want normalised configured journey identifier", data.Message)
	}
}

func TestGetNotificationDataNextStopChanged(t *testing.T) {
	bodyBytes, err := json.Marshal(ctdf.RealtimeJourney{
		Journey:     &ctdf.Journey{DestinationDisplay: "London"},
		NextStopRef: "stop-b",
		NextStop:    &ctdf.Stop{PrimaryName: "Cambridge"},
	})
	if err != nil {
		t.Fatalf("marshal event body: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal event body: %v", err)
	}

	data := GetNotificationData(&ctdf.Event{
		Type: ctdf.EventTypeRealtimeJourneyNextStopChanged,
		Body: body,
	}, ctdf.UserNotificationSubscription{})

	if data.Title != "Next stop changed" {
		t.Fatalf("notification title = %q, want %q", data.Title, "Next stop changed")
	}
	if data.Message != "The service to London is now heading to Cambridge." {
		t.Fatalf("notification message = %q", data.Message)
	}
}
