---
name: go-review
description: Comprehensive Go code review covering idiomatic patterns, concurrency safety, error handling, security, and static analysis. Provides review categories by severity, diagnostic commands, and approval criteria.
---

# Go Code Review

Comprehensive Go-specific code review workflow with severity-based issue categorization.

## When to Activate

- After writing or modifying Go code
- Before committing Go changes
- Reviewing pull requests with Go code
- Onboarding to a new Go codebase
- Learning idiomatic Go patterns

## Review Workflow

1. **Identify Go changes**: `git diff -- '*.go'`
2. **Run static analysis**: `go vet`, `staticcheck`, `golangci-lint`
3. **Security scan**: race conditions, injection, secrets
4. **Concurrency review**: goroutine safety, channels, mutexes
5. **Idiomatic Go check**: conventions and best practices
6. **Generate report**: categorize issues by severity

## Review Categories

### CRITICAL (Must Fix -- Block Merge)

- SQL/Command injection vulnerabilities
- Race conditions without synchronization
- Goroutine leaks (no cancellation mechanism)
- Hardcoded credentials or secrets
- Unsafe pointer usage without justification
- Ignored errors in critical paths (`_, _ := criticalOp()`)
- Insecure TLS (`InsecureSkipVerify: true` in production)
- Unbounded resource consumption (no context timeout)

### HIGH (Should Fix Before Merge)

- Missing error wrapping with context (`return err` without `fmt.Errorf`)
- Panic instead of error returns in library code
- Context not propagated through call chain
- Unbuffered channels causing potential deadlocks
- Missing mutex protection on shared state
- Large functions (>50 lines)
- Non-idiomatic patterns (if/else chains instead of early return)
- Interface pollution (defining large or unused abstractions)
- Missing `defer` for resource cleanup

### MEDIUM (Consider Fixing)

- Non-idiomatic naming (camelCase exports, verbose package names)
- Missing godoc comments on exported symbols
- String concatenation in loops (use `strings.Builder`)
- Slice not preallocated when size is known
- Table-driven tests not used
- Context as non-first parameter
- Deferred call in loop body
- Unused function parameters

## Diagnostic Commands

```bash
# Static analysis
go vet ./...

# Advanced checks
staticcheck ./...
golangci-lint run

# Race detection
go build -race ./...
go test -race ./...

# Security vulnerabilities
govulncheck ./...
gosec ./...
```

## Approval Criteria

| Status | Condition |
|--------|-----------|
| **Approve** | No CRITICAL or HIGH issues |
| **Warning** | Only MEDIUM issues (merge with caution) |
| **Block** | Any CRITICAL or HIGH issues found |

## Review Output Format

```
## Go Code Review Report

### Files Reviewed
- internal/handler/user.go (modified)
- internal/service/auth.go (new)

### Static Analysis Results
- go vet: No issues
- staticcheck: 1 warning

### Issues Found

[CRITICAL] Race Condition
  File: internal/service/auth.go:45
  Issue: Shared map accessed without synchronization
  Fix: Use sync.RWMutex or sync.Map

[HIGH] Missing Error Context
  File: internal/handler/user.go:28
  Issue: Error returned without context
  Fix: return fmt.Errorf("get user %s: %w", userID, err)

### Summary
- CRITICAL: 1 | HIGH: 1 | MEDIUM: 0
- Recommendation: Block merge until CRITICAL issue is fixed
```

## Related

- Agent: `.claude/agents/go-reviewer.md`
- Skill: `golang-patterns`
