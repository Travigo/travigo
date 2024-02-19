package transforms

import (
	"reflect"
	"strings"
)

type TransformDefinition struct {
	Type  string                 `yaml:"Type"`
	Match map[string]string      `yaml:"Match"`
	Data  map[string]interface{} `yaml:"Data"`
}

func (t *TransformDefinition) Transform(inputTypeOf reflect.Type, inputValue reflect.Value, depth int) {
	isMatch := true

	if !inputValue.IsValid() {
		return
	}

	if depth < 0 {
		return
	}

	// Only check values and try and replace them if the types match the transform def
	inputTypeName := strings.Replace(inputTypeOf.String(), "*", "", 1)

	if inputTypeName == t.Type {
		for key, value := range t.Match {
			field := inputValue.FieldByName(key)
			if field.IsValid() {
				if value != field.String() {
					isMatch = false
				}
			} else {
				isMatch = false
			}
		}

		// If we match then go over and update the values
		if isMatch {
			for key, value := range t.Data {
				field := inputValue.FieldByName(key)
				if field.IsValid() {
					valueOf := reflect.ValueOf(value)
					if valueOf.Kind() == reflect.Slice {
						for i := 0; i < valueOf.Len(); i++ {
							item := valueOf.Index(i)
							newSliceValue := reflect.New(field.Type().Elem()).Elem()

							for itemKey, itemValue := range item.Interface().(map[string]interface{}) {
								itemField := newSliceValue.FieldByName(itemKey)
								itemField.Set(reflect.ValueOf(itemValue))
							}

							field.Set(reflect.Append(field, newSliceValue))
						}
					} else {
						field.Set(reflect.ValueOf(value))
					}
				}
			}
		}
	}

	// Go through all the fields and try and run transform against anymore structs/slices
	for i := 0; i < inputValue.NumField(); i++ {
		valueField := inputValue.Field(i)
		typeField := inputValue.Type().Field(i)

		valueTypeKind := typeField.Type.Kind()
		if valueTypeKind == reflect.Pointer {
			valueType := reflect.Indirect(valueField)
			if !valueType.IsValid() {
				continue
			}
			valueTypeKind = valueType.Type().Kind()
		}

		if valueTypeKind == reflect.Slice || valueTypeKind == reflect.Struct {
			Transform(valueField.Interface(), depth-1)
		}
	}
}

func Transform(input interface{}, depth int) {
	inputTypeOf := reflect.TypeOf(input)
	inputValueOf := reflect.ValueOf(input)

	if inputTypeOf.Kind() == reflect.Slice {
		for i := 0; i < inputValueOf.Len(); i++ {
			indexInput := inputValueOf.Index(i).Interface()
			transformValue(reflect.TypeOf(indexInput), reflect.ValueOf(indexInput), depth)
		}
	} else {
		transformValue(inputTypeOf, inputValueOf, depth)
	}
}

func transformValue(inputTypeOf reflect.Type, inputValueOf reflect.Value, depth int) {
	var inputValue reflect.Value
	if inputTypeOf.Kind() == reflect.Pointer {
		inputValue = inputValueOf.Elem()
	}

	for _, transformDef := range transforms {
		transformDef.Transform(inputTypeOf, inputValue, depth)
	}
}
