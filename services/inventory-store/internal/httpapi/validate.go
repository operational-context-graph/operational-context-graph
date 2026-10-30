package httpapi

import "github.com/operational-context-graph/inventory-store/internal/apperr"

// requiredField pairs a field name with its value for required-string validation.
type requiredField struct {
	name  string
	value string
}

// validateRequired returns a *apperr.ValidationError when any of the given
// fields are empty, or nil when all are present.
func validateRequired(fields ...requiredField) error {
	var errs []apperr.FieldError
	for _, f := range fields {
		if f.value == "" {
			errs = append(errs, apperr.FieldError{Field: f.name, Message: "is required"})
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &apperr.ValidationError{Fields: errs}
}
