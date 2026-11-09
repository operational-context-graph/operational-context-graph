#!/usr/bin/env bash
# Configure the git user identity for the GitHub App bot.
#
# Usage: configure-git-identity.sh <app-slug> <app-user-id>
#
# Example:
#   APP_SLUG=$(gh api /app --jq .slug)
#   APP_USER_ID=$(gh api "/users/${APP_SLUG}[bot]" --jq .id)
#   scripts/configure-git-identity.sh "$APP_SLUG" "$APP_USER_ID"

set -euo pipefail

APP_SLUG="${1:?app slug required}"
APP_USER_ID="${2:?app user id required}"

git config user.name "${APP_SLUG}[bot]"
git config user.email "${APP_USER_ID}+${APP_SLUG}[bot]@users.noreply.github.com"
