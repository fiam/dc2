// Package format defines types and interfaces for the supported input/output formats
package format

import (
	"context"
	"net/http"

	"github.com/fiam/dc2/pkg/dc2/api"
)

type Format interface {
	DecodeRequest(r *http.Request) (api.Request, error)
	EncodeError(ctx context.Context, w http.ResponseWriter, e error) error
	EncodeResponse(ctx context.Context, w http.ResponseWriter, resp api.Response) error
}
