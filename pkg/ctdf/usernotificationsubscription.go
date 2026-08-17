package ctdf

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/rs/zerolog/log"
)

// UserNotificationSubscription is a stored notification definition. It is
// intentionally separate from UserEventSubscription until the notification
// delivery pipeline is migrated to use it.
type UserNotificationSubscription struct {
	PrimaryIdentifier string `bson:"primaryidentifier" json:"id" groups:"web-notification-subscription"`
	UserID            string `bson:"userid" json:"-"`

	EventType  EventType                          `bson:"eventtype" json:"eventType" groups:"web-notification-subscription"`
	DaysOfWeek []string                           `bson:"daysofweek,omitempty" json:"daysOfWeek,omitempty" groups:"web-notification-subscription"`
	Values     UserNotificationSubscriptionValues `bson:"values" json:"values" groups:"web-notification-subscription"`

	CreationDateTime     time.Time `bson:"creationdatetime" json:"createdAt" groups:"web-notification-subscription"`
	ModificationDateTime time.Time `bson:"modificationdatetime" json:"updatedAt" groups:"web-notification-subscription"`

	Subject       interface{} `bson:"-" json:"subject,omitempty" groups:"web-notification-subscription"`
	PlatformStops []*Stop     `bson:"-" json:"platformStops,omitempty" groups:"web-notification-subscription"`

	Program *vm.Program `bson:"-" json:"-"`
}

type UserNotificationSubscriptionValues struct {
	ServiceAlertTypes []string `bson:"servicealerttypes" json:"ServiceAlertTypes" groups:"web-notification-subscription"`
	StopRef           string   `bson:"stopref" json:"StopRef" groups:"web-notification-subscription"`
	ServiceRef        string   `bson:"serviceref" json:"ServiceRef" groups:"web-notification-subscription"`
	JourneyRef        string   `bson:"journeyref" json:"JourneyRef" groups:"web-notification-subscription"`
	StopRefs          []string `bson:"stoprefs" json:"StopRefs" groups:"web-notification-subscription"`
	ServiceRefs       []string `bson:"servicerefs,omitempty" json:"ServiceRefs,omitempty" groups:"web-notification-subscription"`
	JourneyRefs       []string `bson:"journeyrefs,omitempty" json:"JourneyRefs,omitempty" groups:"web-notification-subscription"`
	PlatformStopRefs  []string `bson:"platformstoprefs,omitempty" json:"PlatformStopRefs,omitempty" groups:"web-notification-subscription"`
}

var validNotificationDays = map[string]struct{}{
	"Monday":    {},
	"Tuesday":   {},
	"Wednesday": {},
	"Thursday":  {},
	"Friday":    {},
	"Saturday":  {},
	"Sunday":    {},
}

func ValidNotificationDay(day string) bool {
	_, ok := validNotificationDays[day]
	return ok
}

func stringArrayExpression(values []string) string {
	quotedValues := make([]string, len(values))
	for i, value := range values {
		quotedValues[i] = strconv.Quote(value)
	}

	return fmt.Sprintf("[%s]", strings.Join(quotedValues, ", "))
}

func (s *UserNotificationSubscription) serviceAlertExpression() string {
	filters := []string{
		fmt.Sprintf("Body.AlertType in %s", stringArrayExpression(s.Values.ServiceAlertTypes)),
	}

	stopRefs := appendUniqueStrings([]string{s.Values.StopRef}, s.Values.StopRefs)
	serviceRefs := appendUniqueStrings([]string{s.Values.ServiceRef}, s.Values.ServiceRefs)
	journeyRefs := appendUniqueStrings([]string{s.Values.JourneyRef}, s.Values.JourneyRefs)
	referenceFilters := []string{}
	if references := append(stopRefs, serviceRefs...); len(references) > 0 {
		referenceFilters = append(referenceFilters, fmt.Sprintf("# in %s", stringArrayExpression(references)))
	}
	if len(journeyRefs) > 0 {
		suffixFilters := make([]string, 0, len(journeyRefs))
		for _, journeyRef := range journeyRefs {
			suffixFilters = append(suffixFilters, fmt.Sprintf("# endsWith %s", strconv.Quote(":"+journeyRef)))
		}
		referenceFilters = append(referenceFilters, fmt.Sprintf(
			"# in %s || (# startsWith \"DAYINSTANCEOF:\" && (%s))",
			stringArrayExpression(journeyRefs), strings.Join(suffixFilters, " || "),
		))
	}
	if len(referenceFilters) > 0 {
		filters = append(filters, fmt.Sprintf("any(Body.MatchedIdentifiers, {%s})", strings.Join(referenceFilters, " || ")))
	}

	return strings.Join(filters, " && ")
}

func (s *UserNotificationSubscription) realtimeJourneyExpression() string {
	journeyRefs := appendUniqueStrings([]string{s.Values.JourneyRef}, s.Values.JourneyRefs)
	if len(journeyRefs) == 0 {
		return "false"
	}

	return fmt.Sprintf(
		"(Body?.Journey?.PrimaryIdentifier in %[1]s || Body?.RealtimeJourney?.Journey?.PrimaryIdentifier in %[1]s)",
		stringArrayExpression(journeyRefs),
	)
}

func (s *UserNotificationSubscription) realtimeJourneyPlatformExpression() string {
	platformStopRefs := s.Values.PlatformStopRefs
	if len(platformStopRefs) == 0 {
		platformStopRefs = s.Values.StopRefs
	}
	return fmt.Sprintf(
		"%s && Body?.Stop in %s",
		s.realtimeJourneyExpression(),
		stringArrayExpression(platformStopRefs),
	)
}

func appendUniqueStrings(initial []string, values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(initial)+len(values))
	for _, value := range append(initial, values...) {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func (s *UserNotificationSubscription) Compile() error {
	var expression string

	switch s.EventType {
	case EventTypeServiceAlertCreated:
		expression = s.serviceAlertExpression()
	case EventTypeRealtimeJourneyPlatformSet, EventTypeRealtimeJourneyPlatformChanged:
		expression = s.realtimeJourneyPlatformExpression()
	case EventTypeRealtimeJourneyCreated,
		EventTypeRealtimeJourneyActivelyTracked,
		EventTypeRealtimeJourneyCancelled,
		EventTypeRealtimeJourneyOverlayCreated,
		EventTypeRealtimeJourneyLocationTextChanged,
		EventTypeRealtimeJourneyNextStopChanged,
		EventTypeRealtimeJourneyDelayed:
		expression = s.realtimeJourneyExpression()
	default:
		log.Warn().Str("event_type", string(s.EventType)).Msg("Unknown event type for UserNotificationSubscription")
		expression = "false"
	}

	log.Debug().Str("id", s.PrimaryIdentifier).Str("expression", expression).Msg("Compiled UserNotificationSubscription expression")

	program, err := expr.Compile(expression, expr.AsBool(), expr.AllowUndefinedVariables())
	if err != nil {
		return err
	}

	s.Program = program
	return nil
}
