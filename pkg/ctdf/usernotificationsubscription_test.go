package ctdf

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
)

func TestUserNotificationSubscriptionJSONStoresSingleEventType(t *testing.T) {
	subscription := UserNotificationSubscription{
		PrimaryIdentifier: "subscription-1",
		UserID:            "user-1",
		EventType:         EventTypeServiceAlertCreated,
		Values: UserNotificationSubscriptionValues{
			StopRef:           "stop-1",
			ServiceRef:        "service-1",
			JourneyRef:        "journey-1",
			StopRefs:          []string{"stop-1", "stop-2"},
			ServiceAlertTypes: []string{"Delays"},
		},
	}

	data, err := json.Marshal(subscription)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	body := string(data)
	if !strings.Contains(body, `"eventType":"ServiceAlertCreated"`) {
		t.Fatalf("json.Marshal() = %s, want singular eventType", body)
	}
	if strings.Contains(body, `"events"`) {
		t.Fatalf("json.Marshal() = %s, must not contain events array", body)
	}
	if !strings.Contains(body, `"values":{"ServiceAlertTypes":["Delays"],"StopRef":"stop-1","ServiceRef":"service-1","JourneyRef":"journey-1","StopRefs":["stop-1","stop-2"]}`) {
		t.Fatalf("json.Marshal() = %s, want all WebUI values", body)
	}
	if strings.Contains(body, `"userID"`) || strings.Contains(body, `"UserID"`) {
		t.Fatalf("json.Marshal() = %s, must not expose user ID", body)
	}
}

func TestUserNotificationSubscriptionJSONStoresDaysOfWeek(t *testing.T) {
	subscription := UserNotificationSubscription{
		EventType:  EventTypeServiceAlertCreated,
		DaysOfWeek: []string{"Monday", "Friday"},
	}

	data, err := json.Marshal(subscription)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	if body := string(data); !strings.Contains(body, `"daysOfWeek":["Monday","Friday"]`) {
		t.Fatalf("json.Marshal() = %s, want configured days of week", body)
	}
}

func TestUserNotificationSubscriptionCompileServiceAlerts(t *testing.T) {
	tests := []struct {
		name              string
		values            UserNotificationSubscriptionValues
		matchedIdentifier string
	}{
		{
			name: "stop",
			values: UserNotificationSubscriptionValues{
				StopRef: "stop-1",
			},
			matchedIdentifier: "stop-1",
		},
		{
			name: "service",
			values: UserNotificationSubscriptionValues{
				ServiceRef: "service-1",
			},
			matchedIdentifier: "service-1",
		},
		{
			name: "journey",
			values: UserNotificationSubscriptionValues{
				JourneyRef: `journey-"1"`,
			},
			matchedIdentifier: `DAYINSTANCEOF:20260728:journey-"1"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.values.ServiceAlertTypes = []string{
				string(ServiceAlertTypeWarning),
				`type-"requiring-escaping"`,
			}
			subscription := UserNotificationSubscription{
				EventType: EventTypeServiceAlertCreated,
				Values:    test.values,
			}

			assertSubscriptionMatchesEvent(t, &subscription, Event{
				Type: EventTypeServiceAlertCreated,
				Body: ServiceAlert{
					AlertType:          ServiceAlertTypeWarning,
					MatchedIdentifiers: []string{test.matchedIdentifier},
				},
			}, true)

			assertSubscriptionMatchesEvent(t, &subscription, Event{
				Type: EventTypeServiceAlertCreated,
				Body: ServiceAlert{
					AlertType:          ServiceAlertTypeDelays,
					MatchedIdentifiers: []string{test.matchedIdentifier},
				},
			}, false)

			assertSubscriptionMatchesEvent(t, &subscription, Event{
				Type: EventTypeServiceAlertCreated,
				Body: ServiceAlert{
					AlertType:          ServiceAlertTypeWarning,
					MatchedIdentifiers: []string{"another-reference"},
				},
			}, false)
		})
	}
}

func TestUserNotificationSubscriptionCompileRealtimeJourneyEvents(t *testing.T) {
	directBody := func(journeyRef string) interface{} {
		return RealtimeJourney{
			Journey: &Journey{PrimaryIdentifier: journeyRef},
		}
	}
	nestedBody := func(journeyRef string) interface{} {
		return map[string]interface{}{
			"RealtimeJourney": RealtimeJourney{
				Journey: &Journey{PrimaryIdentifier: journeyRef},
			},
		}
	}

	tests := []struct {
		eventType EventType
		body      func(string) interface{}
	}{
		{EventTypeRealtimeJourneyCreated, directBody},
		{EventTypeRealtimeJourneyActivelyTracked, directBody},
		{EventTypeRealtimeJourneyCancelled, directBody},
		{EventTypeRealtimeJourneyOverlayCreated, directBody},
		{EventTypeRealtimeJourneyLocationTextChanged, nestedBody},
		{EventTypeRealtimeJourneyNextStopChanged, nestedBody},
	}

	for _, test := range tests {
		t.Run(string(test.eventType), func(t *testing.T) {
			subscription := UserNotificationSubscription{
				EventType: test.eventType,
				Values: UserNotificationSubscriptionValues{
					JourneyRef: `journey-"1"`,
				},
			}

			assertSubscriptionMatchesEvent(t, &subscription, Event{
				Type: test.eventType,
				Body: test.body(`journey-"1"`),
			}, true)
			assertSubscriptionMatchesEvent(t, &subscription, Event{
				Type: test.eventType,
				Body: test.body("another-journey"),
			}, false)
		})
	}
}

func TestUserNotificationSubscriptionCompileRealtimeJourneyPlatformEvents(t *testing.T) {
	for _, eventType := range []EventType{
		EventTypeRealtimeJourneyPlatformSet,
		EventTypeRealtimeJourneyPlatformChanged,
	} {
		t.Run(string(eventType), func(t *testing.T) {
			subscription := UserNotificationSubscription{
				EventType: eventType,
				Values: UserNotificationSubscriptionValues{
					JourneyRef: `journey-"1"`,
					StopRefs:   []string{"stop-1", `stop-"2"`},
				},
			}

			event := func(journeyRef string, stopRef string) Event {
				return Event{
					Type: eventType,
					Body: map[string]interface{}{
						"RealtimeJourney": RealtimeJourney{
							Journey: &Journey{PrimaryIdentifier: journeyRef},
						},
						"Stop": stopRef,
					},
				}
			}

			assertSubscriptionMatchesEvent(t, &subscription, event(`journey-"1"`, `stop-"2"`), true)
			assertSubscriptionMatchesEvent(t, &subscription, event("another-journey", `stop-"2"`), false)
			assertSubscriptionMatchesEvent(t, &subscription, event(`journey-"1"`, "another-stop"), false)
		})
	}
}

func TestUserNotificationSubscriptionCompileExpandedCommuteReferences(t *testing.T) {
	subscription := UserNotificationSubscription{
		EventType: EventTypeServiceAlertCreated,
		Values: UserNotificationSubscriptionValues{
			ServiceAlertTypes: AllServiceAlertTypes(),
			StopRefs:          []string{"stop-a", "stop-b"},
			ServiceRefs:       []string{"service-a"},
			JourneyRefs:       []string{"journey-a"},
		},
	}
	assertSubscriptionMatchesEvent(t, &subscription, Event{Type: EventTypeServiceAlertCreated, Body: ServiceAlert{AlertType: ServiceAlertTypeDelays, MatchedIdentifiers: []string{"DAYINSTANCEOF:20260817:journey-a"}}}, true)
	assertSubscriptionMatchesEvent(t, &subscription, Event{Type: EventTypeServiceAlertCreated, Body: ServiceAlert{AlertType: ServiceAlertTypeDelays, MatchedIdentifiers: []string{"service-other"}}}, false)

	subscription.EventType = EventTypeRealtimeJourneyPlatformSet
	subscription.Values.PlatformStopRefs = []string{"stop-a"}
	assertSubscriptionMatchesEvent(t, &subscription, Event{Type: EventTypeRealtimeJourneyPlatformSet, Body: map[string]interface{}{"RealtimeJourney": RealtimeJourney{Journey: &Journey{PrimaryIdentifier: "journey-a"}}, "Stop": "stop-a"}}, true)
	assertSubscriptionMatchesEvent(t, &subscription, Event{Type: EventTypeRealtimeJourneyPlatformSet, Body: map[string]interface{}{"RealtimeJourney": RealtimeJourney{Journey: &Journey{PrimaryIdentifier: "journey-a"}}, "Stop": "stop-b"}}, false)
}

func assertSubscriptionMatchesEvent(t *testing.T, subscription *UserNotificationSubscription, event Event, want bool) {
	t.Helper()

	if err := subscription.Compile(); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var consumedEvent Event
	if err := json.Unmarshal(eventData, &consumedEvent); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	output, err := expr.Run(subscription.Program, consumedEvent)
	if err != nil {
		t.Fatalf("expr.Run() error = %v", err)
	}

	if output != want {
		t.Fatalf("expression result = %v, want %v for event %s with body %s", output, want, event.Type, fmt.Sprintf("%#v", consumedEvent.Body))
	}
}
