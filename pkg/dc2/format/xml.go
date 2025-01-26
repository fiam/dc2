package format

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
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
	doc := etree.NewDocument()
	rv := reflect.ValueOf(resp)
	responseType := rv.Type()
	for responseType.Kind() == reflect.Pointer {
		responseType = responseType.Elem()
	}
	root := doc.CreateElement(responseType.Name())
	root.CreateAttr("xmlns", "http://ec2.amazonaws.com/doc/2016-11-15/")
	root.CreateElement("requestId").SetText(api.RequestID(ctx))
	if err := f.encodeResponseFields(root, rv, ""); err != nil {
		return fmt.Errorf("encoding XML response: %w", err)
	}

	doc.Indent(2)
	xmlString, err := doc.WriteToString()
	if err != nil {
		return fmt.Errorf("serializing XML: %w", err)
	}

	api.Logger(ctx).Debug(fmt.Sprintf("response:\n%s\n", xmlString))
	w.Header().Set("Content-Type", "application/xml")
	if _, err := io.WriteString(w, xmlString); err != nil {
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
	case "CreateTags":
		return decodeRequest(r.Form, &api.CreateTagsRequest{})
	case "DeleteTags":
		return decodeRequest(r.Form, &api.DeleteTagsRequest{})
	case "CreateVolume":
		return decodeRequest(r.Form, &api.CreateVolumeRequest{})
	case "DeleteVolume":
		return decodeRequest(r.Form, &api.DeleteVolumeRequest{})
	case "AttachVolume":
		return decodeRequest(r.Form, &api.AttachVolumeRequest{})
	case "DetachVolume":
		return decodeRequest(r.Form, &api.DetachVolumeRequest{})
	case "DescribeVolumes":
		return decodeRequest(r.Form, &api.DescribeVolumesRequest{})
	}
	//nolint
	err := fmt.Errorf("The action '%s' is not valid for this web service.", action)
	return nil, api.ErrWithCode(api.ErrorCodeInvalidAction, err)
}

func (f *XML) encodeResponseFields(el *etree.Element, rv reflect.Value, name string) error {
	switch rv.Kind() {
	case reflect.Struct:
		if t, ok := rv.Interface().(time.Time); ok {
			el.SetText(t.Format(time.RFC3339Nano))
			break
		}
		rt := rv.Type()
		for i := range rt.NumField() {
			typeField := rt.Field(i)
			fieldName := typeField.Tag.Get("xml")
			if fieldName == "" {
				fieldName = typeField.Name
			}
			var innerName string
			if sep := strings.IndexByte(fieldName, '>'); sep >= 0 {
				innerName = fieldName[sep+1:]
				fieldName = fieldName[:sep]
			}
			var fieldElement *etree.Element
			if typeField.Anonymous {
				fieldElement = el
			} else {
				fieldElement = el.CreateElement(fieldName)
			}
			field := rv.Field(i)
			if err := f.encodeResponseFields(fieldElement, field, innerName); err != nil {
				return fmt.Errorf("encoding field %s: %w", fieldName, err)
			}
		}
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return f.encodeResponseFields(el, rv.Elem(), name)
	case reflect.Slice:
		for i := range rv.Len() {
			itemEl := el.CreateElement(name)
			if err := f.encodeResponseFields(itemEl, rv.Index(i), ""); err != nil {
				return fmt.Errorf("encoding item %d: %w", i, err)
			}
		}
	case reflect.String:
		el.SetText(rv.String())
	case reflect.Int, reflect.Int64:
		el.SetText(strconv.FormatInt(rv.Int(), 10))
	case reflect.Uint, reflect.Uint64:
		el.SetText(strconv.FormatUint(rv.Uint(), 10))
	case reflect.Bool:
		el.SetText(strconv.FormatBool(rv.Bool()))
	default:
		return fmt.Errorf("cannot encode type %s", rv.Type())
	}
	return nil
}

func decodeRequest(values url.Values, out api.Request) (api.Request, error) {
	if err := decodeURLEncoded(values, out); err != nil {
		return nil, fmt.Errorf("decoding request: %w", err)
	}
	validate := validator.New(validator.WithRequiredStructEnabled())
	if err := validate.Struct(out); err != nil {
		return nil, fmt.Errorf("validating request: %w", err)
	}
	return out, nil
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
