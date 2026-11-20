---
name: go-build
description: Fix Go build errors, go vet warnings, and linter issues incrementally. Provides diagnostic workflow, common error patterns, fix strategies, and module troubleshooting.
---

# Go Build and Fix

Incrementally fix Go build errors with minimal, surgical changes.

## When to Activate

- `go build ./...` fails with errors
- `go vet ./...` reports issues
- `golangci-lint run` shows warnings
- Module dependencies are broken
- After pulling changes that break the build

## Diagnostic Workflow

Run these in order:

```bash
# 1. Primary build check
go build ./...

# 2. Static analysis
go vet ./...

# 3. Extended linting (if available)
staticcheck ./...
golangci-lint run

# 4. Module issues
go mod verify
go mod tidy -v
```

## Fix Strategy

1. **Build errors first** -- code must compile
2. **Vet warnings second** -- fix suspicious constructs
3. **Lint warnings third** -- style and best practices
4. **One fix at a time** -- verify each change
5. **Minimal changes** -- don't refactor, just fix

## Common Error Patterns

| Error | Typical Fix |
|-------|-------------|
| `undefined: X` | Add import or fix typo |
| `cannot use X as Y` | Type conversion or fix assignment |
| `missing return` | Add return statement |
| `X does not implement Y` | Add missing method |
| `import cycle` | Restructure packages |
| `declared but not used` | Remove or use variable |
| `cannot find package` | `go get` or `go mod tidy` |
| `cannot assign to struct field in map` | Use pointer map or copy-modify-reassign |
| `invalid type assertion` | Only assert from interface types |
| `multiple-value in single-value context` | `result, err := func()` |

## Module Troubleshooting

```bash
# Check for local replace directives
grep "replace" go.mod

# Understand why a module version is selected
go mod why -m <package>

# Pin a specific version
go get <package>@v1.2.3

# Fix checksum issues
go clean -modcache && go mod download

# Show module dependency graph
go mod graph | head -20
```

## Stop Conditions

Stop and report to the user if:
- Same error persists after 3 fix attempts
- Fix introduces more errors than it resolves
- Requires architectural changes beyond scope
- Missing external dependencies that cannot be resolved

## Output Format

```
[FIXED] internal/handler/user.go:42
  Error: undefined: UserService
  Fix: Added import "project/internal/service"
  Remaining errors: 3

Final: Build Status: SUCCESS/FAILED | Errors Fixed: N | Files Modified: list
```

## Key Principles

- Surgical fixes only -- don't refactor, just fix the error
- Never add `//nolint` without explicit approval
- Never change function signatures unless necessary
- Always run `go mod tidy` after adding/removing imports
- Fix root cause over suppressing symptoms

## Related

- Agent: `.claude/agents/go-build-resolver.md`
- Skill: `golang-patterns`
