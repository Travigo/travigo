package ctdf

import (
	"fmt"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/rs/zerolog/log"
)

// UserNotificationSubscription is a stored notification definition. It is
// intentionally separate from UserEventSubscription until the notification
// delivery pipeline is migrated to use it.
type UserNotificationSubscription struct {
	PrimaryIdentifier string `bson:"primaryidentifier" json:"id"`
	UserID            string `bson:"userid" json:"-"`

	EventType EventType                          `bson:"eventtype" json:"eventType"`
	Values    UserNotificationSubscriptionValues `bson:"values" json:"values"`

	CreationDateTime     time.Time `bson:"creationdatetime" json:"createdAt"`
	ModificationDateTime time.Time `bson:"modificationdatetime" json:"updatedAt"`

	Program *vm.Program `bson:"-" json:"-"`
}

type UserNotificationSubscriptionValues struct {
	ServiceAlertTypes []string `bson:"servicealerttypes" json:"serviceAlertTypes"`
	StopRef           string   `bson:"stopref" json:"stopRef"`
	ServiceRef        string   `bson:"serviceref" json:"serviceRef"`
}

func arrayToString(arr []string) string {
	str := "["
	for i, v := range arr {
		if i > 0 {
			str += ", "
		}
		str += fmt.Sprintf("%v", v)
	}
	str += "]"
	return str
}

func (s *UserNotificationSubscription) Compile() error {
	var expression string

	switch s.EventType {
	case EventTypeServiceAlertCreated:
		expression = fmt.Sprintf("Body.AlertType in %s", arrayToString(s.Values.ServiceAlertTypes))
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
