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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	"github.com/go-playground/validator/v10"

	"github.com/fiam/dc2/pkg/dc2/api"
)

type XML struct {
}

const (
	ec2XMLNamespace         = "http://ec2.amazonaws.com/doc/2016-11-15/"
	autoScalingXMLNamespace = "http://autoscaling.amazonaws.com/doc/2011-01-01/"
)

type responseProtocol int

const (
	responseProtocolEC2 responseProtocol = iota + 1
	responseProtocolAutoScaling
)

var requestFactories = map[string]func() api.Request{
	"RunInstances":                  func() api.Request { return &api.RunInstancesRequest{} },
	"DescribeInstances":             func() api.Request { return &api.DescribeInstancesRequest{} },
	"DescribeInstanceStatus":        func() api.Request { return &api.DescribeInstanceStatusRequest{} },
	"StopInstances":                 func() api.Request { return &api.StopInstancesRequest{} },
	"StartInstances":                func() api.Request { return &api.StartInstancesRequest{} },
	"TerminateInstances":            func() api.Request { return &api.TerminateInstancesRequest{} },
	"ModifyInstanceMetadataOptions": func() api.Request { return &api.ModifyInstanceMetadataOptionsRequest{} },
	"DescribeInstanceTypes":         func() api.Request { return &api.DescribeInstanceTypesRequest{} },
	"DescribeInstanceTypeOfferings": func() api.Request { return &api.DescribeInstanceTypeOfferingsRequest{} },
	"GetInstanceTypesFromInstanceRequirements": func() api.Request {
		return &api.GetInstanceTypesFromInstanceRequirementsRequest{}
	},
	"CreateTags":                  func() api.Request { return &api.CreateTagsRequest{} },
	"DeleteTags":                  func() api.Request { return &api.DeleteTagsRequest{} },
	"CreateVolume":                func() api.Request { return &api.CreateVolumeRequest{} },
	"DeleteVolume":                func() api.Request { return &api.DeleteVolumeRequest{} },
	"AttachVolume":                func() api.Request { return &api.AttachVolumeRequest{} },
	"DetachVolume":                func() api.Request { return &api.DetachVolumeRequest{} },
	"DescribeVolumes":             func() api.Request { return &api.DescribeVolumesRequest{} },
	"CreateLaunchTemplate":        func() api.Request { return &api.CreateLaunchTemplateRequest{} },
	"DescribeLaunchTemplates":     func() api.Request { return &api.DescribeLaunchTemplatesRequest{} },
	"DeleteLaunchTemplate":        func() api.Request { return &api.DeleteLaunchTemplateRequest{} },
	"CreateLaunchTemplateVersion": func() api.Request { return &api.CreateLaunchTemplateVersionRequest{} },
	"DescribeLaunchTemplateVersions": func() api.Request {
		return &api.DescribeLaunchTemplateVersionsRequest{}
	},
	"ModifyLaunchTemplate":   func() api.Request { return &api.ModifyLaunchTemplateRequest{} },
	"CreateOrUpdateTags":     func() api.Request { return &api.CreateOrUpdateAutoScalingTagsRequest{} },
	"CreateAutoScalingGroup": func() api.Request { return &api.CreateAutoScalingGroupRequest{} },
	"DescribeAutoScalingGroups": func() api.Request {
		return &api.DescribeAutoScalingGroupsRequest{}
	},
	"UpdateAutoScalingGroup": func() api.Request { return &api.UpdateAutoScalingGroupRequest{} },
	"SetDesiredCapacity":     func() api.Request { return &api.SetDesiredCapacityRequest{} },
	"DetachInstances":        func() api.Request { return &api.DetachInstancesRequest{} },
	"DeleteAutoScalingGroup": func() api.Request { return &api.DeleteAutoScalingGroupRequest{} },
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
	errorMessage := e.Error()
	if apiErr != nil && apiErr.Err != nil {
		errorMessage = apiErr.Err.Error()
	}
	protocol := errorXMLProtocol(api.RequestAction(ctx))
	var errorResponse any
	switch protocol {
	case responseProtocolAutoScaling:
		errorResponse = xmlAutoScalingErrorResponse{
			Error: xmlError{
				Code:    code,
				Message: errorMessage,
			},
			RequestID: api.RequestID(ctx),
		}
	default:
		errorResponse = xmlEC2ErrorResponse{
			Errors: xmlErrors{
				Error: xmlError{
					Code:    code,
					Message: e.Error(),
				},
			},
			RequestID: api.RequestID(ctx),
		}
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
	xmlString, err := encodeResponse(ctx, resp)
	if err != nil {
		return fmt.Errorf("encoding XML response: %w", err)
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
	factory, ok := requestFactories[action]
	if !ok {
		//nolint
		err := fmt.Errorf("The action '%s' is not valid for this web service.", action)
		return nil, api.ErrWithCode(api.ErrorCodeInvalidAction, err)
	}

	return decodeRequest(r.Form, factory())
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

type xmlEC2ErrorResponse struct {
	XMLName   xml.Name  `xml:"Response"`
	Errors    xmlErrors `xml:"Errors"`
	RequestID string    `xml:"RequestID"`
}

type xmlAutoScalingErrorResponse struct {
	XMLName   xml.Name `xml:"ErrorResponse"`
	Error     xmlError `xml:"Error"`
	RequestID string   `xml:"RequestId"`
}

type xmlErrors struct {
	XMLName xml.Name `xml:"Errors"`
	Error   xmlError `xml:"Error"`
}

type xmlError struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func errorXMLProtocol(action string) responseProtocol {
	switch action {
	case "CreateOrUpdateTags",
		"CreateAutoScalingGroup",
		"DescribeAutoScalingGroups",
		"UpdateAutoScalingGroup",
		"SetDesiredCapacity",
		"DetachInstances",
		"DeleteAutoScalingGroup":
		return responseProtocolAutoScaling
	default:
		return responseProtocolEC2
	}
}

func encodeResponse(ctx context.Context, resp api.Response) (string, error) {
	doc := etree.NewDocument()
	rv := reflect.ValueOf(resp)
	responseType := rv.Type()
	for responseType.Kind() == reflect.Pointer {
		responseType = responseType.Elem()
	}
	protocol := responseXMLProtocol(resp)
	root := doc.CreateElement(responseType.Name())
	root.CreateAttr("xmlns", responseXMLNamespace(resp))
	if protocol == responseProtocolEC2 {
		root.CreateElement("RequestId").SetText(api.RequestID(ctx))
	}
	if err := encodeResponseFields(root, rv, ""); err != nil {
		return "", fmt.Errorf("encoding XML response: %w", err)
	}
	if protocol == responseProtocolAutoScaling {
		responseMetadata := root.CreateElement("ResponseMetadata")
		responseMetadata.CreateElement("RequestId").SetText(api.RequestID(ctx))
	}

	doc.Indent(2)
	s, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("serializing XML: %w", err)
	}
	return s, nil
}

func responseXMLNamespace(resp api.Response) string {
	if responseXMLProtocol(resp) == responseProtocolAutoScaling {
		return autoScalingXMLNamespace
	}
	return ec2XMLNamespace
}

func responseXMLProtocol(resp api.Response) responseProtocol {
	switch resp.(type) {
	case api.CreateAutoScalingGroupResponse, *api.CreateAutoScalingGroupResponse,
		api.CreateOrUpdateTagsResponse, *api.CreateOrUpdateTagsResponse,
		api.DescribeAutoScalingGroupsResponse, *api.DescribeAutoScalingGroupsResponse,
		api.UpdateAutoScalingGroupResponse, *api.UpdateAutoScalingGroupResponse,
		api.SetDesiredCapacityResponse, *api.SetDesiredCapacityResponse,
		api.DetachInstancesResponse, *api.DetachInstancesResponse,
		api.DeleteAutoScalingGroupResponse, *api.DeleteAutoScalingGroupResponse:
		return responseProtocolAutoScaling
	default:
		return responseProtocolEC2
	}
}

func encodeResponseFields(el *etree.Element, rv reflect.Value, name string) error {
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
			field := rv.Field(i)
			// Omit nil fields, otherwise the client decode will decode it as zero
			// value, even if the XML is empty.
			if field.Kind() == reflect.Pointer && field.IsNil() {
				continue
			}
			var fieldElement *etree.Element
			if typeField.Anonymous {
				fieldElement = el
			} else {
				fieldElement = el.CreateElement(fieldName)
			}
			if err := encodeResponseFields(fieldElement, field, innerName); err != nil {
				return fmt.Errorf("encoding field %s: %w", fieldName, err)
			}
		}
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return encodeResponseFields(el, rv.Elem(), name)
	case reflect.Slice:
		itemName := name
		if itemName == "" {
			itemName = "item"
		}
		for i := range rv.Len() {
			itemEl := el.CreateElement(itemName)
			if err := encodeResponseFields(itemEl, rv.Index(i), ""); err != nil {
				return fmt.Errorf("encoding item %d: %w", i, err)
			}
		}
	case reflect.Map:
		iter := rv.MapRange()
		keys := make([]string, 0, rv.Len())
		values := make(map[string]reflect.Value, rv.Len())
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			keys = append(keys, key)
			values[key] = iter.Value()
		}
		slices.Sort(keys)
		for _, key := range keys {
			fieldElement := el.CreateElement(key)
			if err := encodeResponseFields(fieldElement, values[key], "item"); err != nil {
				return fmt.Errorf("encoding map field %s: %w", key, err)
			}
		}
	case reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return encodeResponseFields(el, rv.Elem(), name)
	case reflect.String:
		el.SetText(rv.String())
	case reflect.Int, reflect.Int64:
		el.SetText(strconv.FormatInt(rv.Int(), 10))
	case reflect.Uint, reflect.Uint64:
		el.SetText(strconv.FormatUint(rv.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		el.SetText(strconv.FormatFloat(rv.Float(), 'f', -1, 64))
	case reflect.Bool:
		el.SetText(strconv.FormatBool(rv.Bool()))
	default:
		return fmt.Errorf("cannot encode type %s", rv.Type())
	}
	return nil
}
