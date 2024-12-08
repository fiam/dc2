package dc2

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
)

type Action int

const (
	ActionRunInstances Action = iota + 1
	ActionDescribeInstances
	ActionStopInstances
	ActionStartInstances
	ActionTerminateInstances
)

type Request interface {
	Action() Action
}

type CommonRequest struct {
	Action      string `validate:"required"`
	Version     string
	ClientToken string
}

type RunInstancesRequest struct {
	CommonRequest
	ImageID      string `validate:"required"`
	InstanceType string `validate:"required"`
	KeyName      string
	MinCount     int `validate:"required,gt=0"`
	MaxCount     int `validate:"required,gt=0"`
}

func (r RunInstancesRequest) Action() Action { return ActionRunInstances }

type Filter struct {
	Name   *string
	Values []string
}

type DescribeInstancesRequest struct {
	CommonRequest
	Filters     []Filter
	InstanceIDs []string
}

func (r DescribeInstancesRequest) Action() Action { return ActionDescribeInstances }

type StopInstancesRequest struct {
	CommonRequest
	InstanceIDs []string
	DryRun      bool
	Force       bool
}

func (r StopInstancesRequest) Action() Action { return ActionStopInstances }

type StartInstancesRequest struct {
	CommonRequest
	InstanceIDs []string
	DryRun      bool
}

func (r StartInstancesRequest) Action() Action { return ActionStartInstances }

type TerminateInstancesRequest struct {
	CommonRequest
	InstanceIDs []string
	DryRun      bool
}

func (r TerminateInstancesRequest) Action() Action { return ActionTerminateInstances }

func decodeRequest(values url.Values, out Request) (Request, error) {
	rv := reflect.ValueOf(out).Elem()
	var zero reflect.Value
	for k, v := range values {
		fieldName := k
		sliceField, num, hasNumericSuffix := splitNumericSuffix(fieldName)
		if hasNumericSuffix {
			fieldName = sliceField
		}
		f := rv.FieldByNameFunc(func(s string) bool {
			return strings.EqualFold(fieldName, s)
		})
		if f == zero {
			return nil, fmt.Errorf("no %s field found in %T", fieldName, out)
		}
		switch f.Kind() {
		case reflect.String:
			f.SetString(v[0])
		case reflect.Int:
			i, err := strconv.Atoi(v[0])
			if err != nil {
				return nil, fmt.Errorf("parsing int field %s: %w", fieldName, err)
			}
			f.SetInt(int64(i))
		case reflect.Bool:
			v, err := strconv.ParseBool(v[0])
			if err != nil {
				return nil, fmt.Errorf("parsing bool field %s: %w", fieldName, err)
			}
			f.SetBool(v)
		case reflect.Slice:
			if !hasNumericSuffix {
				return nil, fmt.Errorf("slice field %s must have numeric suffix", fieldName)
			}
			if expect := f.Len() + 1; expect != num {
				return nil, fmt.Errorf("expecting index %d for field %s, got %d instead", expect, fieldName, num)
			}
			switch f.Type().Elem().Kind() {
			case reflect.String:
				f.Set(reflect.Append(f, reflect.ValueOf(v[0])))
			default:
				return nil, fmt.Errorf("cannot append slice value on field %s of type %s", fieldName, f.Type().Elem())
			}
		case reflect.Pointer:
			switch f.Type().Elem().Kind() {
			case reflect.Bool:
				v, err := strconv.ParseBool(v[0])
				if err != nil {
					return nil, fmt.Errorf("parsing bool field %s: %w", fieldName, err)
				}
				f.Set(reflect.New(f.Type().Elem()))
				f.Elem().SetBool(v)
			default:
				return nil, fmt.Errorf("cannot set value on field %s of type %s", fieldName, f.Type())
			}
		default:
			return nil, fmt.Errorf("cannot set value on field %s of type %s", fieldName, f.Type())
		}
	}
	validate := validator.New(validator.WithRequiredStructEnabled())
	if err := validate.Struct(out); err != nil {
		return nil, fmt.Errorf("validating request: %w", err)
	}
	return out, nil
}

func parseRequest(r *http.Request) (Request, error) {
	action := r.FormValue("Action")
	switch action {
	case "RunInstances":
		return decodeRequest(r.Form, &RunInstancesRequest{})
	case "DescribeInstances":
		return decodeRequest(r.Form, &DescribeInstancesRequest{})
	case "StopInstances":
		return decodeRequest(r.Form, &StopInstancesRequest{})
	case "StartInstances":
		return decodeRequest(r.Form, &StartInstancesRequest{})
	case "TerminateInstances":
		return decodeRequest(r.Form, &TerminateInstancesRequest{})
	}
	return nil, ErrWithCode(ErrorCodeInvalidAction, fmt.Errorf("The action '%s' is not valid for this web service.", action))
}

func splitNumericSuffix(fieldName string) (string, int, bool) {
	sep := strings.IndexByte(fieldName, '.')
	if sep >= 0 {
		rem := fieldName[sep+1:]
		n, err := strconv.Atoi(rem)
		if err == nil {
			return fieldName[:sep] + "s", n, true
		}
	}
	return "", -1, false
}
