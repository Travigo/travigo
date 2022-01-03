package ctdf

import (
	"crypto/sha256"
	"fmt"
	"time"
)

const OperatorGroupIDFormat = "UK:NOCGRPID:%s"

type OperatorGroup struct {
	Identifier string
	Name       string

	CreationDateTime     time.Time
	ModificationDateTime time.Time
}

func (operatorGroup *OperatorGroup) UniqueHash() string {
	hash := sha256.New()

	hash.Write([]byte(fmt.Sprintf("%s %s",
		operatorGroup.Identifier,
		operatorGroup.Name,
	)))

	return fmt.Sprintf("%x", hash.Sum(nil))
}
