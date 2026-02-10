package format

import (
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

var (
	errZeroIndex   = fmt.Errorf("index <= 0 not allowed")
	errNoSuchField = fmt.Errorf("no such field")
)

func fieldByName(rv reflect.Value, name string) reflect.Value {
	rt := rv.Type()
	for i := range rt.NumField() {
		ft := rt.Field(i)
		if ft.Tag.Get("url") == name {
			return rv.Field(i)
		}
		if ft.Anonymous && rv.Field(i).Kind() == reflect.Struct {
			if f := fieldByName(rv.Field(i), name); f.IsValid() {
				return f
			}
		}
	}
	return reflect.Value{}
}

func decodeURLField(nameComponents []string, values []string, rv reflect.Value) error {
	switch rv.Kind() {
	case reflect.Pointer:
		pv := reflect.New(rv.Type().Elem())
		if err := decodeURLField(nameComponents, values, pv.Elem()); err != nil {
			return err
		}
		rv.Set(pv)
	case reflect.Struct:
		fieldName := nameComponents[0]
		f := fieldByName(rv, fieldName)
		if !f.IsValid() {
			return fmt.Errorf("no %s field found in %s: %w", fieldName, rv.Type().Name(), errNoSuchField)
		}
		if err := decodeURLField(nameComponents[1:], values, f); err != nil {
			return fmt.Errorf("decoding field %s: %w", fieldName, err)
		}
	case reflect.Slice:
		if len(nameComponents) > 0 && strings.EqualFold(nameComponents[0], "member") {
			nameComponents = nameComponents[1:]
		}
		if len(nameComponents) == 0 {
			return fmt.Errorf("missing index for slice %s", rv.Type())
		}
		num, err := strconv.Atoi(nameComponents[0])
		if err != nil {
			return fmt.Errorf("parsing index: %w", err)
		}
		if num <= 0 {
			return errZeroIndex
		}

		if num > rv.Len() {
			if num > rv.Len()+1 {
				return fmt.Errorf("expecting index <= %d, got %d instead", rv.Len()+1, num)
			}
			rv.Set(reflect.Append(rv, reflect.New(rv.Type().Elem()).Elem()))
		}
		if err := decodeURLField(nameComponents[1:], values, rv.Index(num-1)); err != nil {
			return err
		}
	case reflect.String:
		rv.SetString(values[0])
	case reflect.Int:
		i, err := strconv.Atoi(values[0])
		if err != nil {
			return fmt.Errorf("parsing int field: %w", err)
		}
		rv.SetInt(int64(i))
	case reflect.Bool:
		v, err := strconv.ParseBool(values[0])
		if err != nil {
			return fmt.Errorf("parsing bool field: %w", err)
		}
		rv.SetBool(v)
	default:
		return fmt.Errorf("cannot set value of type %s", rv.Type())
	}
	return nil
}

func decodeURLEncoded(values url.Values, out any) error {
	rv := reflect.ValueOf(out).Elem()
	keys := slices.Collect(maps.Keys(values))
	slices.SortFunc(keys, func(a, b string) int {
		if len(a) < len(b) {
			return -1
		}
		if len(a) > len(b) {
			return 1
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	for _, k := range keys {
		v := values[k]
		components := strings.Split(k, ".")
		if err := decodeURLField(components, v, rv); err != nil {
			return fmt.Errorf("decoding URL encoded value %s: %w", k, err)
		}
	}
	return nil
}
