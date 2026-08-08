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

	EventType EventType                          `bson:"eventtype" json:"eventType" groups:"web-notification-subscription"`
	Values    UserNotificationSubscriptionValues `bson:"values" json:"values" groups:"web-notification-subscription"`

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

	for _, reference := range []string{s.Values.StopRef, s.Values.ServiceRef} {
		if reference != "" {
			filters = append(filters, fmt.Sprintf("%s in Body.MatchedIdentifiers", strconv.Quote(reference)))
		}
	}

	if s.Values.JourneyRef != "" {
		filters = append(filters, fmt.Sprintf(
			"any(Body.MatchedIdentifiers, {# == %[1]s || (# startsWith \"DAYINSTANCEOF:\" && # endsWith %[2]s)})",
			strconv.Quote(s.Values.JourneyRef),
			strconv.Quote(":"+s.Values.JourneyRef),
		))
	}

	return strings.Join(filters, " && ")
}

func (s *UserNotificationSubscription) realtimeJourneyExpression() string {
	if s.Values.JourneyRef == "" {
		return "false"
	}

	journeyRef := strconv.Quote(s.Values.JourneyRef)
	return fmt.Sprintf(
		"(Body?.Journey?.PrimaryIdentifier == %[1]s || Body?.RealtimeJourney?.Journey?.PrimaryIdentifier == %[1]s)",
		journeyRef,
	)
}

func (s *UserNotificationSubscription) realtimeJourneyPlatformExpression() string {
	return fmt.Sprintf(
		"%s && Body?.Stop in %s",
		s.realtimeJourneyExpression(),
		stringArrayExpression(s.Values.StopRefs),
	)
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
		EventTypeRealtimeJourneyNextStopChanged:
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
