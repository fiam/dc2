package format

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/go-playground/validator/v10"
)

type XML struct {
}

func (f *XML) DecodeRequest(r *http.Request) (api.Request, error) {
	if r.Method != http.MethodPost {
		return nil, api.ErrWithCode(api.ErrorCodeMethodNotAllowed, nil)
	}
	if err := r.ParseForm(); err != nil {
		return nil, api.ErrWithCode(api.ErrorCodeInvalidForm, err)
	}
	api.Logger(r.Context()).Debug(fmt.Sprintf("received request %s %s %+v\n", r.Method, r.URL.Path, r.Form))
	return f.parseRequest(r)
}

func (f *XML) EncodeError(ctx context.Context, w http.ResponseWriter, e error) error {
	var code string
	var apiErr *api.Error
	if errors.As(e, &apiErr) {
		code = apiErr.Code
	}
	statusCode := http.StatusBadRequest
	switch code {
	case api.ErrorCodeMethodNotAllowed:
		statusCode = http.StatusMethodNotAllowed
	case "":
		// Unknown error
		statusCode = http.StatusInternalServerError
	}
	errorResponse := xmlErrorResponse{
		Errors: xmlErrors{
			Error: xmlError{
				Code:    code,
				Message: e.Error(),
			},
		},
		RequestID: api.RequestID(ctx),
	}

	logger := api.Logger(ctx)

	xmlData, err := xml.MarshalIndent(errorResponse, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing XML error: %w", err)
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(statusCode)
	if _, err := w.Write(xmlData); err != nil {
		return fmt.Errorf("writing response to client: %w", err)
	}
	logger.Debug(fmt.Sprintf("returning error with status code %d:\n%s\n", statusCode, string(xmlData)))
	return nil
}

func (f *XML) EncodeResponse(ctx context.Context, w http.ResponseWriter, resp api.Response) error {
	data, err := xml.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing XML response: %w", err)
	}

	api.Logger(ctx).Debug(fmt.Sprintf("response:\n%s\n", string(data)))
	w.Header().Set("Content-Type", "application/xml")
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("writing response to client: %w", err)
	}
	return nil
}

func (f *XML) parseRequest(r *http.Request) (api.Request, error) {
	action := r.FormValue("Action")
	switch action {
	case "RunInstances":
		return decodeRequest(r.Form, &api.RunInstancesRequest{})
	case "DescribeInstances":
		return decodeRequest(r.Form, &api.DescribeInstancesRequest{})
	case "StopInstances":
		return decodeRequest(r.Form, &api.StopInstancesRequest{})
	case "StartInstances":
		return decodeRequest(r.Form, &api.StartInstancesRequest{})
	case "TerminateInstances":
		return decodeRequest(r.Form, &api.TerminateInstancesRequest{})
	}
	//nolint
	err := fmt.Errorf("The action '%s' is not valid for this web service.", action)
	return nil, api.ErrWithCode(api.ErrorCodeInvalidAction, err)
}

func decodeRequest(values url.Values, out api.Request) (api.Request, error) {
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

type xmlErrorResponse struct {
	XMLName   xml.Name  `xml:"Response"`
	Errors    xmlErrors `xml:"Errors"`
	RequestID string    `xml:"RequestID"`
}

type xmlErrors struct {
	XMLName xml.Name `xml:"Errors"`
	Error   xmlError `xml:"Error"`
}

type xmlError struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}
