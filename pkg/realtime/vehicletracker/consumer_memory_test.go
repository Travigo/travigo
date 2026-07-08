package vehicletracker

import (
	"testing"

	"github.com/adjust/rmq/v5"
)

func TestBatchConsumerDiscardsMalformedPayloadWithoutRejecting(t *testing.T) {
	for _, payload := range []string{"not-json", "null"} {
		t.Run(payload, func(t *testing.T) {
			delivery := rmq.NewTestDeliveryString(payload)

			(&BatchConsumer{}).Consume(rmq.Deliveries{delivery})

			if delivery.State != rmq.Acked {
				t.Fatalf("expected malformed delivery to be acknowledged, got %v", delivery.State)
			}
		})
	}
}
