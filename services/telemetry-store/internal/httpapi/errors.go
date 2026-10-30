package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/operational-context-graph/telemetry-store/internal/apperr"
)

const (
	errTypePayloadTooLarge      = "payload-too-large"
	errTypeInvalidInput         = "invalid-input"
	errTypeUnsupportedMediaType = "unsupported-media-type"
	errTypeNotFound             = "not-found"
	errTypeConflict             = "conflict"
	errTypeServiceUnavailable   = "service-unavailable"
	errTypeBadGateway           = "bad-gateway"
	errTypeInternal             = "internal"
)

// problem is an RFC 9457 problem details object.
type problem struct {
	Type   string              `json:"type"`
	Title  string              `json:"title"`
	Status int                 `json:"status"`
	Detail string              `json:"detail,omitempty"`
	Errors []apperr.FieldError `json:"errors,omitempty"`
}

func renderError(c *gin.Context, err error) {
	var p problem
	var ve *apperr.ValidationError
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxErr):
		p = problem{
			Type:   errTypePayloadTooLarge,
			Title:  "Payload Too Large",
			Status: http.StatusRequestEntityTooLarge,
		}
	case errors.As(err, &ve) || errors.Is(err, apperr.ErrInvalidInput):
		p = problem{
			Type:   errTypeInvalidInput,
			Title:  "Bad Request",
			Status: http.StatusBadRequest,
			Detail: err.Error(),
		}
		if ve != nil {
			p.Errors = ve.Fields
		}
	case errors.Is(err, apperr.ErrUnsupportedMediaType):
		p = problem{
			Type:   errTypeUnsupportedMediaType,
			Title:  "Unsupported Media Type",
			Status: http.StatusUnsupportedMediaType,
			Detail: err.Error(),
		}
	case errors.Is(err, apperr.ErrNotFound):
		p = problem{
			Type:   errTypeNotFound,
			Title:  "Not Found",
			Status: http.StatusNotFound,
			Detail: err.Error(),
		}
	case errors.Is(err, apperr.ErrAlreadyExists), errors.Is(err, apperr.ErrConflict):
		p = problem{
			Type:   errTypeConflict,
			Title:  "Conflict",
			Status: http.StatusConflict,
			Detail: err.Error(),
		}
	case errors.Is(err, apperr.ErrLabelExists):
		// Label already committed — idempotent success, not an error.
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, gin.H{"status": "already_exists"})
		return
	case errors.Is(err, apperr.ErrDorisOverloaded):
		c.Header("Retry-After", "5")
		p = problem{
			Type:   errTypeServiceUnavailable,
			Title:  "Service Unavailable",
			Status: http.StatusServiceUnavailable,
			Detail: "Doris is under memory pressure; retry after back-off",
		}
	case errors.Is(err, apperr.ErrDorisLoadFailed):
		p = problem{
			Type:   errTypeBadGateway,
			Title:  "Bad Gateway",
			Status: http.StatusBadGateway,
			Detail: err.Error(),
		}
	default:
		route, _ := c.Get(routeKey)
		requestID := c.GetHeader("X-Request-Id")
		requestID = strings.ReplaceAll(requestID, "\n", "")
		requestID = strings.ReplaceAll(requestID, "\r", "")
		slog.Error("unhandled error", "err", err, "route", route, "request_id", requestID)
		p = problem{
			Type:   errTypeInternal,
			Title:  "Internal Server Error",
			Status: http.StatusInternalServerError,
		}
	}
	c.Header("Content-Type", "application/problem+json")
	c.JSON(p.Status, p)
}
