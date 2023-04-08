package dataaggregator

import (
	"reflect"
)

type DataSource interface {
	GetName() string
	Supports() []reflect.Type
	Lookup(any) (interface{}, error)
}
