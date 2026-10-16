// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package offline

import (
	"testing"

	"github.com/piprate/json-gold/ld"
	"github.com/stretchr/testify/require"
)

func TestLoadersRejectExternalReferences(t *testing.T) {
	_, err := (JSONSchemaLoader{}).Load("https://example.test/schema.json")
	require.ErrorIs(t, err, errExternalReference)
	_, err = (JSONLDLoader{}).LoadDocument("file:///tmp/context.jsonld")
	require.ErrorIs(t, err, errExternalReference)
	options := ld.NewJsonLdOptions("")
	options.DocumentLoader = JSONLDLoader{}
	_, err = options.DocumentLoader.LoadDocument("https://example.test/context.jsonld")
	require.ErrorIs(t, err, errExternalReference)
}

func TestValidateInputSize(t *testing.T) {
	require.NoError(t, ValidateInputSize(make([]byte, 16*1024*1024)))
	require.Error(t, ValidateInputSize(make([]byte, 16*1024*1024+1)))
}
