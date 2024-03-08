package formats

import (
	"io"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/ctdf"
)

type Format interface {
	ParseFile(io.Reader) error
	Import(string, SupportedObjects, *ctdf.DataSource) error
}

type RealtimeQueueFormat interface {
	Format
	SetupRealtimeQueue(rmq.Queue)
}
