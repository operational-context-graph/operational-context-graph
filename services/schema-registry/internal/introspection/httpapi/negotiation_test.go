package httpapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNegotiateMediaType verifies absent Accept defaults, quality and specificity precedence, wildcard matching, and rejection when no representation is acceptable.
func TestNegotiateMediaType(t *testing.T) {
	available := []string{"application/json", "application/schema+json"}
	cases := []struct {
		name, accept, want string
		available          []string
		ok                 bool
	}{
		{name: "absent", available: available, want: "application/json", ok: true},
		{name: "wildcard", accept: "*/*", available: available, want: "application/json", ok: true},
		{name: "exact", accept: "application/schema+json", available: available, want: "application/schema+json", ok: true},
		{name: "quality zero", accept: "application/json;q=0", available: available},
		{name: "quality", accept: "application/json;q=0.1, application/schema+json;q=0.9", available: available, want: "application/schema+json", ok: true},
		{name: "specific zero overrides wildcard", accept: "application/json;q=0, */*;q=1", available: available, want: "application/schema+json", ok: true},
		{name: "empty value", accept: ",", available: available},
		{name: "matching subtype wildcard", accept: "application/*", available: []string{"application/json"}, want: "application/json", ok: true},
		{name: "nonmatching subtype wildcard", accept: "text/*", available: []string{"application/json"}},
		{name: "quality over range", accept: "application/json;q=2", available: []string{"application/json"}},
		{name: "malformed quality", accept: "application/json;q=bad", available: []string{"application/json"}},
		{name: "malformed available media type", accept: "application/*", available: []string{"invalid media type"}},
		{name: "no available representations"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := NegotiateMediaType(test.accept, test.available...)
			assert.Equal(t, test.want, got)
			assert.Equal(t, test.ok, ok)
		})
	}
}
