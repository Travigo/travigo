package transforms

import (
	"fmt"
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
				if value != fmt.Sprint(field.Interface()) {
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
			setTransformField(field, value)
		}
	}
}

func setTransformField(field reflect.Value, value interface{}) {
	if !field.CanSet() {
		return
	}

	switch field.Kind() {
	case reflect.Slice:
		setTransformSlice(field, value)
	case reflect.Map:
		setTransformMap(field, value)
	case reflect.Struct:
		if data, ok := value.(map[string]interface{}); ok {
			handleSubDocument(field, data)
		}
	default:
		valueOf := reflect.ValueOf(value)
		if valueOf.IsValid() && valueOf.Type().AssignableTo(field.Type()) {
			field.Set(valueOf)
		} else if valueOf.IsValid() && valueOf.Type().ConvertibleTo(field.Type()) {
			field.Set(valueOf.Convert(field.Type()))
		}
	}
}

func setTransformMap(field reflect.Value, value interface{}) {
	data, ok := value.(map[string]interface{})
	if !ok {
		return
	}

	if field.IsNil() {
		field.Set(reflect.MakeMap(field.Type()))
	}

	for key, itemValue := range data {
		keyValue := reflect.ValueOf(key)
		valueValue := reflect.ValueOf(itemValue)
		if keyValue.Type().ConvertibleTo(field.Type().Key()) {
			keyValue = keyValue.Convert(field.Type().Key())
		}
		if valueValue.IsValid() && valueValue.Type().ConvertibleTo(field.Type().Elem()) {
			valueValue = valueValue.Convert(field.Type().Elem())
		}
		if keyValue.Type().AssignableTo(field.Type().Key()) && valueValue.IsValid() && valueValue.Type().AssignableTo(field.Type().Elem()) {
			field.SetMapIndex(keyValue, valueValue)
		}
	}
}

func setTransformSlice(field reflect.Value, value interface{}) {
	valueOf := reflect.ValueOf(value)
	if !valueOf.IsValid() || valueOf.Kind() != reflect.Slice {
		return
	}

	for i := 0; i < valueOf.Len(); i++ {
		item := valueOf.Index(i).Interface()
		newSliceValue := reflect.New(field.Type().Elem()).Elem()

		if itemMap, ok := item.(map[string]interface{}); ok && newSliceValue.Kind() == reflect.Struct {
			for itemKey, itemValue := range itemMap {
				itemField := newSliceValue.FieldByName(itemKey)
				if itemField.IsValid() {
					setTransformField(itemField, itemValue)
				}
			}
		} else {
			itemValue := reflect.ValueOf(item)
			if itemValue.IsValid() && itemValue.Type().AssignableTo(field.Type().Elem()) {
				newSliceValue.Set(itemValue)
			} else if itemValue.IsValid() && itemValue.Type().ConvertibleTo(field.Type().Elem()) {
				newSliceValue.Set(itemValue.Convert(field.Type().Elem()))
			} else {
				continue
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
