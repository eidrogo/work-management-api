# First-Person Documentation Design

**Date:** 2026-08-14
**Status:** Approved for planning

## Goal

I will rewrite the repository's human-facing documentation so that I speak in
the first-person singular when I describe my interpretation, decisions,
implementation, verification, trade-offs, and deferrals. I will preserve the
existing technical meaning and evidence.

## Scope

I will review all 16 tracked documentation files:

- `README.md`
- `docs/architecture.md`
- `docs/operations.md`
- `docs/debrief.md`
- `docs/assessment/*.md`
- `docs/adr/*.md`

I will not change Go code, tests, SQL, OpenAPI, shell scripts, workflow files,
or internal `.superpowers/**` state as part of the voice rewrite.

## Approaches Considered

1. **Blind pronoun replacement:** This is fast, but I rejected it because it
   would incorrectly turn managers, workers, PostgreSQL, SMTP, Mailpit, and
   other runtime actors into the author.
2. **Reflection-only rewrite:** This is low risk, but I rejected it because most
   of the architecture, operations, ADR, and verification narrative would
   remain impersonal.
3. **Selective authorial rewrite:** I chose this approach. I will use `I` and
   `my` only where I am the grammatical agent, while keeping the actual subject
   for runtime behavior and third-party actions.

## Voice Rules

I will convert author-owned statements such as:

- "The selected design uses..." to "I selected..."
- "The implementation provides..." to "I implemented..."
- "The verification showed..." to "I verified..."
- "The following work was deferred..." to "I deferred..."

I will keep factual runtime subjects intact, for example:

- "The worker claims one delivery."
- "PostgreSQL provides the transaction boundary."
- "Mailpit proves SMTP acceptance, not external inbox delivery."

I will use first-person singular, not the editorial `we`.

## Protected Content

I will not rewrite literal or contract-like content:

- fenced commands, output, JSON, SQL, and Mermaid diagrams
- tables, schemas, identifiers, routes, status codes, and error codes
- required assessment questions and quoted requirements
- hashes, dates, scan IDs, file paths, URLs, and PASS/FAIL evidence
- ADR metadata, headings, links, and supersession history
- instructions where the reader, operator, manager, worker, API, database,
  SMTP peer, Mailpit, or reviewer is the real actor

I may rewrite the prose introducing protected content, but I will not alter the
protected content itself.

## Verification

I will verify the change with:

1. a file-by-file diff review against the 16-file inventory;
2. a voice audit for impersonal authorial phrases and accidental first-person
   plural;
3. a protected-content comparison for fenced blocks and required reflection
   questions;
4. Markdown/link/style checks already available in the repository;
5. the repository's standard static/documentation gates; and
6. a clean Git diff limited to documentation and its verification artifacts.

## Success Criteria

- I speak in first-person singular wherever I describe my own work.
- Runtime actors retain their correct technical subjects.
- Commands, evidence, contracts, and historical facts remain unchanged.
- I introduce no new architectural, operational, or verification claim.
- Every changed line traces directly to the requested voice change or its
  verification.
