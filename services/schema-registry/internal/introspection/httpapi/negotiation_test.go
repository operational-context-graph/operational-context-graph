// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestNegotiateMediaType(t *testing.T) {
	cases := []struct {
		name, accept, want string
		ok                 bool
	}{
		{"absent", "", "application/json", true}, {"wildcard", "*/*", "application/json", true}, {"exact", "application/schema+json", "application/schema+json", true}, {"quality zero", "application/json;q=0", "", false}, {"quality", "application/json;q=0.1, application/schema+json;q=0.9", "application/schema+json", true}, {"specific zero overrides wildcard", "application/json;q=0, */*;q=1", "application/schema+json", true}, {"empty value", ",", "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := NegotiateMediaType(test.accept, "application/json", "application/schema+json")
			assert.Equal(t, test.want, got)
			assert.Equal(t, test.ok, ok)
		})
	}
}
