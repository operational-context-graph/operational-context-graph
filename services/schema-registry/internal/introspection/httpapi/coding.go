// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/representation"
)

type ContentCoding string

const (
	Identity ContentCoding = "identity"
	Gzip     ContentCoding = "gzip"

	// MaxIdentityRepresentationBytes is the version 1 identity JSON limit.
	MaxIdentityRepresentationBytes = representation.MaxGeneratedJSONBytes
)

var ErrRepresentationTooLarge = errors.New("identity representation exceeds 64 MiB")

// NegotiateCoding selects identity or deterministic gzip.
func NegotiateCoding(acceptEncoding string) (ContentCoding, bool) {
	if strings.TrimSpace(acceptEncoding) == "" {
		return Identity, true
	}
	qualities := parseCodingQualities(acceptEncoding)
	gzipQuality, gzipSpecified := qualities["gzip"]
	if !gzipSpecified {
		gzipQuality, gzipSpecified = qualities["*"]
	}
	identity, identitySpecified := qualities["identity"]
	if !identitySpecified {
		// Identity remains acceptable unless identity, or all unspecified
		// codings through a zero-quality wildcard, explicitly excludes it.
		if wildcard, found := qualities["*"]; found && wildcard == 0 {
			identity = 0
		} else if gzipSpecified && gzipQuality > 0 {
			// Prefer a requested supported coding when identity is implicit.
			identity = -1
		} else {
			identity = 1
		}
	}
	if gzipQuality > identity && gzipQuality > 0 {
		return Gzip, true
	}
	if identity > 0 {
		return Identity, true
	}
	return "", false
}

func parseCodingQualities(header string) map[string]float64 {
	qualities := make(map[string]float64)
	for _, item := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(item), ";")
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(fields[0]))
		if name == "" {
			continue
		}
		q := 1.0
		for _, field := range fields[1:] {
			field = strings.TrimSpace(field)
			if strings.HasPrefix(strings.ToLower(field), "q=") {
				value, err := strconv.ParseFloat(field[2:], 64)
				if err != nil || value < 0 || value > 1 {
					q = 0
				} else {
					q = value
				}
			}
		}
		if previous, found := qualities[name]; !found || q > previous {
			qualities[name] = q
		}
	}
	return qualities
}

// EncodeRepresentation returns either identity bytes or a deterministic RFC 1952 stream.
func EncodeRepresentation(identity []byte, coding ContentCoding) ([]byte, error) {
	return EncodeRepresentationContext(context.Background(), identity, coding)
}

// EncodeRepresentationContext returns either identity bytes or a deterministic
// RFC 1952 stream. It checks cancellation between bounded write chunks.
func EncodeRepresentationContext(ctx context.Context, identity []byte, coding ContentCoding) ([]byte, error) {
	if len(identity) > MaxIdentityRepresentationBytes {
		return nil, ErrRepresentationTooLarge
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if coding == Identity {
		return append([]byte(nil), identity...), nil
	}
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	writer.ModTime = time.Unix(0, 0)
	writer.OS = 255
	for len(identity) > 0 {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			return nil, err
		}
		count := min(len(identity), 32*1024)
		if _, err := writer.Write(identity[:count]); err != nil {
			_ = writer.Close()
			return nil, err
		}
		identity = identity[count:]
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func decompress(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	return io.ReadAll(reader)
}
