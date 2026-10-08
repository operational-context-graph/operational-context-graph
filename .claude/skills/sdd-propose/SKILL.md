---
name: sdd-propose
description: Enumerate failure modes for a new component, then run /opsx:propose (OpenSpec) with those failure modes folded into the description so the generated spec covers edge cases, not just the happy path. Use when building a new component, library, or interface with any of: non-obvious boundary conditions (concurrent access, timeout/retry, partial failure, ordering invariants); a spec that serves as a design document (multiple consumers, security-relevant interface, traceable requirement); a long-lived contract (extended, versioned, or depended on by other packages); or team coding standards that must be applied consistently. Skip for a simple, short-lived, single-consumer utility.
---

# sdd-propose

Generate an OpenSpec proposal for a new component in two steps: first enumerate how the
component can fail (the *elicitation* step), then run `/opsx:propose` with those failure
modes written into the description. A proposal built this way seeds the spec with WHEN/THEN
scenarios for concurrency, boundary inputs, and error paths — the cases a plain happy-path
prompt leaves out.

**What is elicitation?** A structured self-analysis, done before proposing, that lists every
way the component can go wrong: concurrent callers, boundary inputs (nil, empty, zero,
max-size), dependency failures, and configuration extremes. Each failure mode becomes at
least one explicit scenario in the spec. This is the step that determines edge-case depth,
so do not skip it.

Invoke this skill when adding a new component, library, or interface where any of these hold:

- The component has non-obvious boundary conditions (concurrent access, timeout/retry,
  partial failure, ordering invariants).
- The spec will serve as a design document — multiple consumers, a security-relevant
  interface, or a requirement that must be traceable.
- The interface contract is long-lived — it will be extended, versioned, or depended on by
  other packages.
- Team coding standards must be applied consistently (library choices, security constraints,
  architectural patterns).

For a simple, short-lived, single-consumer utility, a plain prompt is enough — skip this skill.

## Inputs

The user's message provides the requirement description — what to build. This becomes
the basis for both the elicitation and the propose invocation.

If the requirement is ambiguous about scope or the component's external interface, ask clarifying questions before starting.

## Steps

### 1. Verify OpenSpec

```sh
openspec --version
```

If the command fails, tell the user to install OpenSpec:
```sh
npm install -g @fission-ai/openspec@latest
```
Do not proceed until `openspec --version` exits 0.

### 2. Verify or scaffold constitution

Check whether `openspec/config.yaml` exists.

If it does not exist, tell the user to grab the most recent version from the [repository-template](https://github.tools.sap/autonomous-operations/repository-template).

If it already exists, read it briefly to confirm it has a `context` block. If the file
contains only `schema: spec-driven` with no `context` or `rules` (empty constitution),
warn the user:

> Constitution is empty — edge-case depth will be low. Add
> failure-mode enumeration rules before proceeding.

Continue regardless of the warning — do not block.

### 3. Initialize OpenSpec (if needed)

Check whether `.claude/commands/opsx/` exists. If not, run:

```sh
openspec init
```

Select Claude Code as the AI tool when prompted.

### 4. Pre-propose elicitation

This is the key step. Do not skip it for non-trivial requirements.

For the requirement provided by the user, enumerate all failure modes across these four
categories. Write findings to `openspec/elicitation-<YYYY-MM-DD>.md`.

**Concurrent access**
- Which methods modify shared state?
- What happens when two goroutines (or requests) call them simultaneously?
- Are there read-modify-write sequences that must be atomic?

**Boundary inputs**
- nil, empty string, zero, negative values
- Single-element collections, max-size collections
- Expired entries, zero-TTL, zero-capacity
- Large inputs (1MB+ payloads, >10K entries)

**Error and failure paths**
- What happens when dependencies are unavailable?
- What is the contract on partial success?
- What errors should be wrapped vs. returned as-is?
- Are there panic/recover boundaries needed?

**Configuration extremes**
- What happens with zero-value config (`Config{}`)?
- What happens with contradictory settings?
- Are defaults correct when fields are omitted?

**Explicit non-goals**
- What is explicitly NOT in scope?
- What should a future reader know NOT to extend this component to do?

The elicitation output MUST include at least one item per category. If a category
genuinely does not apply (e.g. a purely functional, stateless component has no
concurrent access concern), state that explicitly — do not leave the category blank.

### 5. Run /opsx:propose (enriched)

Run `/opsx:propose` with a description that includes BOTH the original requirement AND
the specific failure modes and boundary conditions from the elicitation. Do not use a
generic happy-path description.

Structure the propose description as:

```
<original requirement text>

Failure modes to cover (from pre-propose elicitation):
- Concurrent: <enumerate>
- Boundary: <enumerate>
- Error paths: <enumerate>
- Non-goals: <enumerate>
```

This seeds the spec with the right WHEN/THEN scenario surface before OpenSpec generates
the artifact.

### 6. Capture elicitation reference

After `/opsx:propose` completes, append a one-line reference to the elicitation file
inside the generated `proposal.md` (under a `## Elicitation` heading if not already
present):

```
Elicitation: openspec/elicitation-<date>.md
```

### 7. Confirm

Print:
```
sdd-propose complete: elicitation → openspec/elicitation-<date>.md
                      proposal → <path-to-proposal.md>
```

---
