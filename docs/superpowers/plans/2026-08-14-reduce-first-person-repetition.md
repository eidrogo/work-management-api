# Reduce First-Person Repetition Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce repetitive first-person phrasing across repository documentation without removing content or changing technical meaning.

**Architecture:** Rewrite author-owned prose in three independent document groups. Keep one personal ownership marker where needed, then use the real technical subject or parallel verb phrases. Protect literal evidence and verify the final branch against the pre-rewrite baseline.

**Tech Stack:** Markdown, Git, Python 3 audit helpers, existing repository verification commands.

## Global Constraints

- Keep first person for explicit ownership, judgment, accountability, and AI disclosure.
- Do not delete files, sections, requirements, evidence, commands, diagrams, tables, links, or technical detail.
- Preserve conditional language for deferred work.
- Keep runtime actors as their real subjects.
- Reduce first-person matching lines by at least 50% from the 332-line baseline.
- Use branch `feature/reduce-first-person-repetition` and author `eidrogo <142827281+eidrogo@users.noreply.github.com>`.

---

### Task 1: Establish the voice and protected-content baseline

**Files:**
- Create: `.superpowers/sdd/2026-08-14-reduce-first-person-repetition/baseline.json` (ignored evidence)
- Create: `.superpowers/sdd/2026-08-14-reduce-first-person-repetition/protected-before.json` (ignored evidence)

**Interfaces:**
- Consumes: Markdown at design parent `b72c406c6819027baf07ed96f8d995317a314e0f`
- Produces: baseline counts and protected-content hashes used by Tasks 2–4

- [ ] **Step 1: Capture the baseline**

Write a temporary Python audit that scans tracked `*.md`, ignores fenced blocks and table rows for prose metrics, and records:

```json
{"matching_lines": 332, "prose_i_occurrences": 460, "prose_lines_beginning_i": 258}
```

Record actual measured values without changing the approved 332-line success baseline.

- [ ] **Step 2: Hash protected content**

Extract fenced blocks, table rows, required reflection headings, ADR metadata, and exact evidence identifiers into `protected-before.json`. Store SHA-256 per file and category.

- [ ] **Step 3: Verify the evidence is reproducible**

Run the audit twice and compare output byte-for-byte.

Expected: identical JSON outputs and a clean tracked worktree.

### Task 2: Rewrite primary reviewer-facing documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/operations.md`
- Modify: `docs/debrief.md`

**Interfaces:**
- Consumes: Task 1 baseline and the approved style rules
- Produces: concise primary documentation with unchanged content

- [ ] **Step 1: Record the RED voice metric for these files**

Run the audit against the four files and save the matching-line and sentence-opening counts.

- [ ] **Step 2: Rewrite author-owned prose**

Apply these transformations manually and contextually:

```text
I chose X. I built Y. I made Z. -> I chose X, built Y, and made Z.
For audit, I would extend... -> Audit: extend...
I implemented: / I implemented... -> I implemented: / Implemented...
```

Keep personal ownership in reflections and AI disclosure where it adds meaning.

- [ ] **Step 3: Verify Task 2**

Run the voice audit, protected-content comparison, `git diff --check`, and Markdown link check.

Expected: fewer repeated first-person openings, no protected-content changes, and no broken links.

- [ ] **Step 4: Commit Task 2**

```bash
git add README.md docs/architecture.md docs/operations.md docs/debrief.md
git commit -m "docs: reduce first-person repetition in core guides"
```

### Task 3: Rewrite ADRs and assessment documentation

**Files:**
- Modify: `docs/adr/*.md`
- Modify: `docs/assessment/*.md`
- Modify: `docs/superpowers/specs/*.md`
- Modify: `docs/superpowers/plans/*.md`

**Interfaces:**
- Consumes: Task 1 baseline and Task 2 style
- Produces: consistent voice across every remaining tracked Markdown file

- [ ] **Step 1: Record the RED voice metric for remaining files**

Run the audit and identify repeated `I would`, `I rejected`, `I accepted`, `I implemented`, and `I used` constructions.

- [ ] **Step 2: Rewrite ADR prose**

Use one ownership marker per section where useful:

```text
I rejected: <parallel alternatives>
I accepted: <parallel consequences>
I will revisit when: <parallel conditions>
```

Keep ADR metadata and the 0005-to-0006 supersession history unchanged.

- [ ] **Step 3: Rewrite assessment and process prose**

Coordinate author actions, use direct technical subjects, and preserve all verification chronology, hashes, counts, scan IDs, and PASS/FAIL claims.

- [ ] **Step 4: Verify Task 3**

Run the voice audit, protected-content comparison, `git diff --check`, link check, and a complete word-diff review against `b72c406`.

Expected: total first-person matching lines at or below 166, with no semantic drift.

- [ ] **Step 5: Commit Task 3**

```bash
git add docs
git commit -m "docs: balance first-person voice across records"
```

### Task 4: Final verification, review, and publication

**Files:**
- Update: `.superpowers/sdd/2026-08-14-reduce-first-person-repetition/` (ignored evidence)

**Interfaces:**
- Consumes: completed documentation branch
- Produces: verified, reviewed, pushed branch

- [ ] **Step 1: Run documentation gates**

Run the final voice metric, protected-content hashes, word-diff review, link check, `git diff --check`, and repository secret/OpenAPI checks.

- [ ] **Step 2: Run full authority**

```bash
make verify-full
```

Expected: all normal and race PostgreSQL tests, lint, builds, OpenAPI, secrets, Docker image, Compose, Mailpit demo, cleanup, and clean-tree checks pass.

- [ ] **Step 3: Request independent review**

Require the reviewer to inspect the entire documentation diff for pronoun repetition, unnatural passive voice, semantic drift, actor attribution, future-versus-implemented tense, and evidence integrity.

- [ ] **Step 4: Push after approval**

```bash
git push -u origin feature/reduce-first-person-repetition
```

Verify remote SHA and GitHub Actions at the exact pushed commit.
