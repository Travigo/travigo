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

func TestGetNotificationDataRealtimeJourneyLifecycle(t *testing.T) {
	tests := []struct {
		eventType ctdf.EventType
		body      map[string]interface{}
		title     string
		message   string
	}{
		{
			eventType: ctdf.EventTypeRealtimeJourneyCreated,
			body: map[string]interface{}{
				"Journey": map[string]interface{}{"DestinationDisplay": "London"},
			},
			title:   "Journey created",
			message: "Live data is now available for the service to London.",
		},
		{
			eventType: ctdf.EventTypeRealtimeJourneyActivelyTracked,
			body: map[string]interface{}{
				"Journey": map[string]interface{}{"DestinationDisplay": "London"},
			},
			title:   "Live tracking started",
			message: "Live tracking has started for the service to London.",
		},
		{
			eventType: ctdf.EventTypeRealtimeJourneyLocationTextChanged,
			body: map[string]interface{}{
				"Journey":                    map[string]interface{}{"DestinationDisplay": "London"},
				"VehicleLocationDescription": "Approaching Cambridge",
			},
			title:   "Location changed",
			message: "The service to London is now at Approaching Cambridge.",
		},
	}

	for _, test := range tests {
		t.Run(string(test.eventType), func(t *testing.T) {
			data := GetNotificationData(&ctdf.Event{
				Type: test.eventType,
				Body: test.body,
			}, ctdf.UserNotificationSubscription{})

			if data.Title != test.title {
				t.Fatalf("notification title = %q, want %q", data.Title, test.title)
			}
			if data.Message != test.message {
				t.Fatalf("notification message = %q, want %q", data.Message, test.message)
			}
		})
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

func TestNotificationTargetURLServiceAlert(t *testing.T) {
	tests := []struct {
		name    string
		values  ctdf.UserNotificationSubscriptionValues
		matched []interface{}
		want    string
	}{
		{
			name:    "stop",
			values:  ctdf.UserNotificationSubscriptionValues{StopRef: "stop-1"},
			matched: []interface{}{"stop-1"},
			want:    "/stops/stop-1",
		},
		{
			name:    "service",
			values:  ctdf.UserNotificationSubscriptionValues{ServiceRef: "service:1"},
			matched: []interface{}{"service:1"},
			want:    "/services/service:1",
		},
		{
			name:    "dated journey compact",
			values:  ctdf.UserNotificationSubscriptionValues{JourneyRef: "journey-1"},
			matched: []interface{}{"DAYINSTANCEOF:20260811:journey-1"},
			want:    "/journeys/journey-1?date=2026-08-11",
		},
		{
			name:    "dated journey ISO",
			values:  ctdf.UserNotificationSubscriptionValues{JourneyRef: "journey-1"},
			matched: []interface{}{"DAYINSTANCEOF:2026-08-11:journey-1"},
			want:    "/journeys/journey-1?date=2026-08-11",
		},
		{
			name:    "no configured match",
			values:  ctdf.UserNotificationSubscriptionValues{ServiceRef: "service-1"},
			matched: []interface{}{"service-2"},
			want:    "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := notificationTargetURL(&ctdf.Event{
				Type: ctdf.EventTypeServiceAlertCreated,
				Body: map[string]interface{}{"MatchedIdentifiers": test.matched},
			}, ctdf.UserNotificationSubscription{Values: test.values})

			if got != test.want {
				t.Fatalf("notification target URL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNotificationTargetURLRealtimeJourney(t *testing.T) {
	tests := []struct {
		name string
		body map[string]interface{}
		want string
	}{
		{
			name: "realtime journey",
			body: map[string]interface{}{
				"Journey":        map[string]interface{}{"PrimaryIdentifier": "journey:1"},
				"JourneyRunDate": "2026-08-11T00:00:00Z",
			},
			want: "/journeys/journey:1?date=2026-08-11",
		},
		{
			name: "platform update",
			body: map[string]interface{}{
				"RealtimeJourney": map[string]interface{}{
					"Journey":        map[string]interface{}{"PrimaryIdentifier": "journey-2"},
					"JourneyRunDate": "2026-08-12T00:00:00Z",
				},
			},
			want: "/journeys/journey-2?date=2026-08-12",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := notificationTargetURL(&ctdf.Event{Body: test.body}, ctdf.UserNotificationSubscription{})
			if got != test.want {
				t.Fatalf("notification target URL = %q, want %q", got, test.want)
			}
		})
	}
}
