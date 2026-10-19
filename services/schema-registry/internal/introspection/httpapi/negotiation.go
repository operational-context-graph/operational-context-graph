// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"mime"
	"strconv"
	"strings"
)

// NegotiateMediaType selects the first available representation with the best
// client quality. An absent Accept header accepts every available type.
func NegotiateMediaType(accept string, available ...string) (string, bool) {
	if strings.TrimSpace(accept) == "" {
		if len(available) == 0 {
			return "", false
		}
		return available[0], true
	}
	bestType, bestQuality := "", -1.0
	for _, candidate := range available {
		candidateQuality, candidateSpecificity := -1.0, -1
		for _, rangeValue := range strings.Split(accept, ",") {
			mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(rangeValue))
			if err != nil {
				continue
			}
			quality := 1.0
			if raw, ok := parameters["q"]; ok {
				quality, err = strconv.ParseFloat(raw, 64)
				if err != nil || quality < 0 || quality > 1 {
					continue
				}
			}
			specificity := mediaRangeSpecificity(mediaType, candidate)
			if specificity > candidateSpecificity || specificity == candidateSpecificity && quality > candidateQuality {
				candidateQuality, candidateSpecificity = quality, specificity
			}
		}
		if candidateSpecificity >= 0 && candidateQuality > bestQuality {
			bestType, bestQuality = candidate, candidateQuality
		}
	}
	if bestQuality <= 0 {
		return "", false
	}
	return bestType, true
}

func mediaRangeSpecificity(mediaRange, candidate string) int {
	if mediaRange == "*/*" {
		return 0
	}
	parsed, _, err := mime.ParseMediaType(candidate)
	if err != nil {
		return -1
	}
	if strings.HasSuffix(mediaRange, "/*") {
		if strings.HasPrefix(parsed, strings.TrimSuffix(mediaRange, "*")) {
			return 1
		}
		return -1
	}
	if strings.EqualFold(mediaRange, parsed) {
		return 2
	}
	return -1
}
