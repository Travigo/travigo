package transforms

import (
	"reflect"
	"strings"
)

type TransformDefinition struct {
	Type  string                 `yaml:"Type"`
	Group string                 `yaml:"Group"`
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
			// pretty.Println(inputValue, t.Data)
			handleSubDocument(inputValue, t.Data)
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

func handleSubDocument(inputValue reflect.Value, data map[string]interface{}) {
	for key, value := range data {
		field := inputValue.FieldByName(key)
		if field.IsValid() {
			valueOf := reflect.ValueOf(value)
			if valueOf.Kind() == reflect.Slice {
				handleSubDocument2(field, valueOf, data)
			} else if valueOf.Kind() == reflect.Map {
				handleMap(field, value.(map[string]interface{}))
			} else {
				field.Set(reflect.ValueOf(value))
			}
		}
	}
}

func handleMap(field reflect.Value, data map[string]interface{}) {
	for key, value := range data {
		field.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(value))
	}
}

func handleSubDocument2(field reflect.Value, valueOf reflect.Value, data map[string]interface{}) {
	for i := 0; i < valueOf.Len(); i++ {
		item := valueOf.Index(i)
		newSliceValue := reflect.New(field.Type().Elem()).Elem()

		for itemKey, itemValue := range item.Interface().(map[string]interface{}) {
			itemField := newSliceValue.FieldByName(itemKey)
			itemValueOf := reflect.ValueOf(itemValue)

			if itemValueOf.Kind() == reflect.Slice {
				for i2 := 0; i2 < itemValueOf.Len(); i2++ {
					// itemStuckInALoopOfDoom := itemValueOf.Index(i2)
					// pretty.Println(i, itemStuckInALoopOfDoom.Interface().(map[string]interface{}))
					// pretty.Println(itemField.String(), newSliceValue.String(), itemStuckInALoopOfDoom.Interface().(map[string]interface{}))
					// handleSubDocument2(newSliceValue, itemField, itemStuckInALoopOfDoom.Interface().(map[string]interface{}))
				}
			} else {
				itemField.Set(itemValueOf)
			}
		}

		field.Set(reflect.Append(field, newSliceValue))
	}
}

func Transform(input interface{}, depth int, groups ...string) {
	inputTypeOf := reflect.TypeOf(input)
	inputValueOf := reflect.ValueOf(input)

	// TODO just supporting 1 group atm
	group := ""
	if len(groups) == 1 {
		group = groups[0]
	}

	if inputTypeOf.Kind() == reflect.Slice {
		for i := 0; i < inputValueOf.Len(); i++ {
			indexInput := inputValueOf.Index(i).Interface()
			transformValue(reflect.TypeOf(indexInput), reflect.ValueOf(indexInput), depth, group)
		}
	} else {
		transformValue(inputTypeOf, inputValueOf, depth, group)
	}
}

func transformValue(inputTypeOf reflect.Type, inputValueOf reflect.Value, depth int, group string) {
	var inputValue reflect.Value
	if inputTypeOf.Kind() == reflect.Pointer {
		inputValue = inputValueOf.Elem()
	}

	for _, transformDef := range transforms {
		if transformDef.Group == group {
			transformDef.Transform(inputTypeOf, inputValue, depth)
		}
	}
}
