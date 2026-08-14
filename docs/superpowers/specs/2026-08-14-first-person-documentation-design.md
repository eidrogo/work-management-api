# First-Person Documentation Design

**Date:** 2026-08-14
**Status:** Implemented

## Goal

I will rewrite the repository's human-facing documentation in first-person
singular for submitter interpretation, decisions, implementation, verification,
trade-offs, and deferrals while preserving the existing technical meaning and
evidence.

## Scope

All 16 tracked documentation files will be reviewed:

- `README.md`
- `docs/architecture.md`
- `docs/operations.md`
- `docs/debrief.md`
- `docs/assessment/*.md`
- `docs/adr/*.md`

Go code, tests, SQL, OpenAPI, shell scripts, workflow files, and internal
`.superpowers/**` state will remain outside the voice rewrite.

## Approaches Considered

I rejected two alternatives before choosing the third:

1. **Blind pronoun replacement:** Fast, but it would incorrectly turn managers,
   workers, PostgreSQL, SMTP, Mailpit, and
   other runtime actors into the author.
2. **Reflection-only rewrite:** Low risk, but most
   of the architecture, operations, ADR, and verification narrative would
   remain impersonal.
3. **Selective authorial rewrite:** The chosen approach will use `I` and `my` only
   where the submitter is the grammatical agent, while keeping the actual
   subject for runtime behavior and third-party actions.

## Voice Rules

I will convert author-owned statements such as:

- "The selected design uses..." to "I selected..."
- "The implementation provides..." to "I implemented..."
- "The verification showed..." to "I verified..."
- "The following work was deferred..." to "I deferred..."

Factual runtime subjects will remain intact, for example:

- "The worker claims one delivery."
- "PostgreSQL provides the transaction boundary."
- "The exercised worker/Mailpit flow proves SMTP acceptance, not external inbox delivery."

The authorial voice will use first-person singular, not the editorial `we`.

## Protected Content

Literal or contract-like content will remain unchanged:

- fenced commands, output, JSON, SQL, and Mermaid diagrams
- tables, schemas, identifiers, routes, status codes, and error codes
- required assessment questions and quoted requirements
- hashes, dates, scan IDs, file paths, URLs, and PASS/FAIL evidence
- ADR metadata, headings, links, and supersession history
- instructions where the reader, operator, manager, worker, API, database,
  SMTP peer, Mailpit, or reviewer is the real actor

I may rewrite prose introducing protected content, but not the protected
content itself.

## Verification

Verification will use:

1. a file-by-file diff review against the 16-file inventory;
2. a voice audit for impersonal authorial phrases and accidental first-person
   plural;
3. a protected-content comparison for fenced blocks and required reflection
   questions;
4. Markdown/link/style checks already available in the repository;
5. the repository's standard static/documentation gates; and
6. a clean Git diff limited to documentation and its verification artifacts.

## Success Criteria

- First-person singular identifies the submitter's own work.
- Runtime actors retain their correct technical subjects.
- Commands, evidence, contracts, and historical facts remain unchanged.
- The rewrite introduces no new architectural, operational, or verification claim.
- Every changed line traces directly to the requested voice change or its
  verification.
