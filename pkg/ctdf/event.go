package ctdf

type Event struct {
	Type EventType
	Body interface{}
}

type EventType string

const (
	EventTypeServiceAlertCreated EventType = "ServiceAlertCreated"
)
