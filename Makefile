# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0
.PHONY: lint-proto format-proto generate

lint-proto:
	buf lint

format-proto:
	buf format -w

generate:
	buf generate
