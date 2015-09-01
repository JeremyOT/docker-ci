package structflag

import (
	"flag"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func makeFlagName(prefix, flagName string) string {
	return prefix + strings.Replace(flagName, "_", "-", -1)
}

func parseFlagTag(field reflect.StructField) (name string, description string, defaultValue string) {
	tagValue := field.Tag.Get("flag")
	if tagValue == "" {
		return
	}
	components := strings.Split(tagValue, ",")
	name = components[0]
	if len(components) > 1 {
		description = components[1]
	}
	if len(components) > 2 {
		defaultValue = components[2]
	}
	return
}

var durationType = reflect.TypeOf(time.Duration(0))

// StructToArgs converts a struct into a list of arguments of the form '-field-name=value'.
// If prefix is not empty, args will take the form '-prefix-field-name=value'. String values
// are quoted, bool, int and float values are not. Note that not all types supported by
// StructToArgs may be unpacked by StructToFlags.
//
// A flag:"name" tag may be applied to the struct fields to set a custom field name. If no
// matching tag is found, structflag will try to use a "json" tag. Otherwise the field name
// is used as is. Finally, all underscores in the field name are replaced with '-'.
func StructToArgs(prefix string, v interface{}) (args []string) {
	if prefix != "" {
		prefix = prefix + "-"
	}
	value := reflect.ValueOf(v)
	if value.Kind() == reflect.Ptr {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		log.Panicln("Can't call StructToArgs with value:", v, "type:", value.Type())
	}
	typ := value.Type()
	fields := value.NumField()
	args = make([]string, 0, fields)
	for i := 0; i < fields; i++ {
		stringValue := ""
		field := typ.Field(i)
		flagName := field.Name
		flagTagName, _, _ := parseFlagTag(field)
		if flagTagName != "" {
			flagName = flagTagName
		} else {
			jsonName := field.Tag.Get("json")
			if jsonName != "" {
				flagName = strings.Split(jsonName, ",")[0]
			}
		}
		flagName = strings.Replace(flagName, "_", "-", -1)
		fieldValue := value.Field(i)
		switch fieldValue.Kind() {
		case reflect.Bool:
			fallthrough
		case reflect.Int:
			fallthrough
		case reflect.Int8:
			fallthrough
		case reflect.Int16:
			fallthrough
		case reflect.Int32:
			fallthrough
		case reflect.Int64:
			if fieldValue.Type() == durationType {
				stringValue = fmt.Sprintf("%v", fieldValue.Interface())
				break
			}
			fallthrough
		case reflect.Uint:
			fallthrough
		case reflect.Uint8:
			fallthrough
		case reflect.Uint16:
			fallthrough
		case reflect.Uint32:
			fallthrough
		case reflect.Uint64:
			fallthrough
		case reflect.Uintptr:
			fallthrough
		case reflect.Float32:
			fallthrough
		case reflect.Float64:
			stringValue = fmt.Sprintf("%v", fieldValue.Interface())
		default:
			stringValue = strconv.Quote(fmt.Sprintf("%v", fieldValue.Interface()))
		}
		args = append(args, fmt.Sprintf("-%s=%s", makeFlagName(prefix, flagName), stringValue))
	}
	return
}

// StructToFlags registers the fields of a struct with the flag package so they may be set
// with arguments of the form '-field-name=value'. If prefix is not empty, args will take
//
// A flag:"name,description,defaultValue" tag may be applied to the struct fields to set a
// custom field name, description and defaultValue. If no matching tag is found, structflag
// will try to use a "json" tag for a field name. Otherwise the field name is used as is.
// Finally, all underscores in the field name are replaced with '-'.
// Default values are parsed using normal string conversion methods for the value type.
//
// Supported field types: bool, int, int64, uint,uint64, float64, time.Duration, string
// Calling StructToFlags with a struct containing unsupported fields will panic.
func StructToFlags(prefix string, v interface{}) {
	if prefix != "" {
		prefix = prefix + "-"
	}
	value := reflect.ValueOf(v)
	if value.Kind() != reflect.Ptr {
		log.Panicln("Can't call StructToFlags with value:", v, "type:", value.Type(), "must be a pointer to a struct.")
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		log.Panicln("Can't call StructToFlags with value:", v, "type:", value.Type(), "must be a pointer to a struct.")
	}

	typ := value.Type()
	fields := value.NumField()
	for i := 0; i < fields; i++ {
		field := typ.Field(i)
		flagName := field.Name
		flagTagName, flagDescription, flagDefaultValue := parseFlagTag(field)
		if flagTagName != "" {
			flagName = flagTagName
		} else {
			jsonName := field.Tag.Get("json")
			if jsonName != "" {
				flagName = strings.Split(jsonName, ",")[0]
			}
		}
		flagName = strings.Replace(flagName, "_", "-", -1)
		fieldValue := value.Field(i)
		switch fieldValue.Kind() {
		case reflect.Bool:
			def, _ := strconv.ParseBool(flagDefaultValue)
			flag.BoolVar(fieldValue.Addr().Interface().(*bool), makeFlagName(prefix, flagName), def, flagDescription)
		case reflect.Int:
			def, _ := strconv.Atoi(flagDefaultValue)
			flag.IntVar(fieldValue.Addr().Interface().(*int), makeFlagName(prefix, flagName), def, flagDescription)
		case reflect.Int64:
			if fieldValue.Type() == durationType {
				def, _ := time.ParseDuration(flagDefaultValue)
				flag.DurationVar(fieldValue.Addr().Interface().(*time.Duration), makeFlagName(prefix, flagName), def, flagDescription)
			} else {
				def, _ := strconv.ParseInt(flagDefaultValue, 10, 64)
				flag.Int64Var(fieldValue.Addr().Interface().(*int64), makeFlagName(prefix, flagName), def, flagDescription)
			}
		case reflect.Uint:
			def, _ := strconv.ParseUint(flagDefaultValue, 10, 64)
			flag.UintVar(fieldValue.Addr().Interface().(*uint), makeFlagName(prefix, flagName), uint(def), flagDescription)
		case reflect.Uint64:
			def, _ := strconv.ParseUint(flagDefaultValue, 10, 64)
			flag.Uint64Var(fieldValue.Addr().Interface().(*uint64), makeFlagName(prefix, flagName), def, flagDescription)
		case reflect.Float64:
			def, _ := strconv.ParseFloat(flagDefaultValue, 64)
			flag.Float64Var(fieldValue.Addr().Interface().(*float64), makeFlagName(prefix, flagName), def, flagDescription)
		case reflect.String:
			flag.StringVar(fieldValue.Addr().Interface().(*string), makeFlagName(prefix, flagName), flagDefaultValue, flagDescription)
		default:
			log.Panicln("Invalid field type:", field)
		}
	}
	return

}
