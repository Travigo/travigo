package redis_client

import (
	"github.com/adjust/rmq/v5"
	"testing"
)

type countingQueueConnection struct {
	rmq.Connection
	opens int
}

func (c *countingQueueConnection) OpenQueue(name string) (rmq.Queue, error) {
	c.opens++
	return c.Connection.OpenQueue(name)
}

func TestEventsQueueHandleReused(t *testing.T) {
	oldConnection, oldQueue := QueueConnection, eventsQueue
	defer func() { QueueConnection, eventsQueue = oldConnection, oldQueue }()
	connection := &countingQueueConnection{Connection: rmq.NewTestConnection()}
	QueueConnection = connection
	eventsQueue = nil
	first, err := GetEventsQueue()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		queue, err := GetEventsQueue()
		if err != nil || queue != first {
			t.Fatal("events queue handle not reused")
		}
	}
	if connection.opens != 1 {
		t.Fatalf("opened queue %d times, want 1", connection.opens)
	}
}
