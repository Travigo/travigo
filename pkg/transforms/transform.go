package transforms

import (
	"reflect"
)

type TransformDefinition struct {
	Type  string
	Match map[string]string
	Data  map[string]interface{}
}

func (t *TransformDefinition) Transform(inputTypeOf reflect.Type, inputValue reflect.Value) {
	isMatch := true

	if !inputValue.IsValid() {
		return
	}

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
				field.Set(reflect.ValueOf(value))
				// field.SetString(value)
			}
		}

		// for i := 0; i < inputValue.NumField(); i++ {
		// 	valueField := inputValue.Field(i)
		// 	typeField := inputValue.Type().Field(i)

		// 	if t.Data[typeField.Name] != "" {
		// 		valueField.SetString(t.Data[typeField.Name])
		// 	}

		// 	valueType := typeField.Type.Kind()
		// 	if valueType == reflect.Pointer {
		// 		valueType = reflect.Indirect(valueField).Type().Kind()
		// 	}

		// 	pretty.Println(typeField.Name, valueType == reflect.Slice, valueType == reflect.Struct)

		// 	if valueType == reflect.Slice || valueType == reflect.Struct {
		// 		Transform(valueField.Interface())
		// 	}
		// }
	}

	for i := 0; i < inputValue.NumField(); i++ {
		valueField := inputValue.Field(i)
		typeField := inputValue.Type().Field(i)

		valueTypeKind := typeField.Type.Kind()
		if valueTypeKind == reflect.Pointer {
			valueType := reflect.Indirect(valueField)
			if !valueType.IsValid() {
				return
			}
			valueTypeKind = valueType.Type().Kind()
		}

		if valueTypeKind == reflect.Slice || valueTypeKind == reflect.Struct {
			Transform(valueField.Interface())
		}
	}
}

func Transform(input interface{}) {
	inputTypeOf := reflect.TypeOf(input)
	inputValueOf := reflect.ValueOf(input)

	if inputTypeOf.Kind() == reflect.Slice {
		for i := 0; i < inputValueOf.Len(); i++ {
			indexInput := inputValueOf.Index(i).Interface()
			transformValue(reflect.TypeOf(indexInput), reflect.ValueOf(indexInput))
		}
	} else {
		transformValue(inputTypeOf, inputValueOf)
	}
}

func transformValue(inputTypeOf reflect.Type, inputValueOf reflect.Value) {
	// inputTypeName := strings.Replace(inputTypeOf.String(), "*", "", 1)

	var inputValue reflect.Value
	if inputTypeOf.Kind() == reflect.Pointer {
		inputValue = inputValueOf.Elem()
	}

	for _, transformDef := range transforms {
		// if inputTypeName != transformDef.Type {
		// 	continue
		// }

		transformDef.Transform(inputTypeOf, inputValue)
	}
}
