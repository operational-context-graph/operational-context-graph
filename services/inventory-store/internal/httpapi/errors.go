package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
)

// problem is an RFC 9457 problem details object.
type problem struct {
	Type   string              `json:"type"`
	Title  string              `json:"title"`
	Status int                 `json:"status"`
	Detail string              `json:"detail,omitempty"`
	Errors []apperr.FieldError `json:"errors,omitempty"`
}

const (
	problemTypeRequestEntityTooLarge = "request-entity-too-large"
	problemTypeUnsupportedMediaType  = "unsupported-media-type"
	problemTypeInvalidInput          = "invalid-input"
	problemTypeNotFound              = "not-found"
	problemTypeConflict              = "conflict"
	problemTypeInternal              = "internal"
)

func sanitizeForLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func renderError(c *gin.Context, err error) {
	var p problem
	var ve *apperr.ValidationError
	var maxBytesErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytesErr):
		p = problem{
			Type:   problemTypeRequestEntityTooLarge,
			Title:  "Request Entity Too Large",
			Status: http.StatusRequestEntityTooLarge,
		}
	case errors.Is(err, apperr.ErrUnsupportedMediaType):
		p = problem{
			Type:   problemTypeUnsupportedMediaType,
			Title:  "Unsupported Media Type",
			Status: http.StatusUnsupportedMediaType,
			Detail: err.Error(),
		}
	case errors.As(err, &ve) || errors.Is(err, apperr.ErrInvalidInput):
		p = problem{
			Type:   problemTypeInvalidInput,
			Title:  "Bad Request",
			Status: http.StatusBadRequest,
			Detail: err.Error(),
		}
		if ve != nil {
			p.Errors = ve.Fields
		}
	case errors.Is(err, apperr.ErrNotFound):
		p = problem{
			Type:   problemTypeNotFound,
			Title:  "Not Found",
			Status: http.StatusNotFound,
			Detail: err.Error(),
		}
	case errors.Is(err, apperr.ErrAlreadyExists), errors.Is(err, apperr.ErrConflict):
		p = problem{
			Type:   problemTypeConflict,
			Title:  "Conflict",
			Status: http.StatusConflict,
			Detail: err.Error(),
		}
	default:
		route, _ := c.Get(routeKey)
		requestID, _ := c.Get(requestIDKey)
		slog.Error("unhandled error", "err", sanitizeForLog(err.Error()), "route", route, "request_id", requestID)
		p = problem{
			Type:   problemTypeInternal,
			Title:  "Internal Server Error",
			Status: http.StatusInternalServerError,
		}
	}
	c.Header("Content-Type", "application/problem+json")
	c.JSON(p.Status, p)
}

// bindErr converts a ShouldBindJSON error into the right apperr sentinel.
// *http.MaxBytesError passes through so renderError maps it to 413;
// everything else is wrapped as ErrInvalidInput (400).
func bindErr(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return mbe
	}
	return fmt.Errorf("%w: %s", apperr.ErrInvalidInput, err.Error())
}
