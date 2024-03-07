package formats

import (
	"io"

	"github.com/travigo/travigo/pkg/ctdf"
)

type Format interface {
	ParseFile(io.Reader) error
	ImportIntoMongoAsCTDF(*ctdf.DataSource) error
}
