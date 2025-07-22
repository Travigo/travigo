package ctdf

import (
	"io"
	"time"
)

type LinkableRecord interface {
	GenerateDeterministicID(io.Writer)
	GetCreationDateTime() time.Time
	GetPrimaryIdentifier() string
	SetPrimaryIdentifier(string)
	SetOtherIdentifiers([]string)
}

type BaseRecord struct {
	PrimaryIdentifier string
	OtherIdentifiers  []string
}
