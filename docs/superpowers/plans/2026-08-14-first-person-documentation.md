# First-Person Documentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** I will rewrite all submitter-owned documentation prose in first-person singular without changing technical behavior, literal evidence, or the identity of runtime actors.

**Architecture:** I will edit only the 16 tracked submission-documentation files, grouped by reader purpose; snapshot protected fenced content and required reflection questions before editing; review the diff and run a targeted voice audit after each group; and finish with the repository's existing static and documentation gates.

**Tech Stack:** Markdown, Git, Python 3 standard library, ripgrep, Go repository verification commands.

## Global Constraints

I will apply these constraints:

- use first-person singular (`I`, `me`, `my`), never editorial `we` or `our`;
- use first person only when the submitter is the grammatical agent: interpretation, decisions, implementation, verification, trade-offs, and deferrals;
- retain the real subject for managers, workers, clients, the API, PostgreSQL, SMTP, Mailpit, reviewers, and other runtime actors;
- keep fenced content, commands, output, diagrams, tables, identifiers, routes, states, error codes, hashes, dates, scan IDs, URLs, required reflection questions, and PASS/FAIL facts unchanged;
- keep recommendations and production prerequisites distinct from implemented claims;
- keep ADR 0005 historical and superseded by ADR 0006;
- leave non-Markdown repository files and `.superpowers/**` internal state unchanged;
- make no architectural, operational, security, or verification claim beyond the existing documentation; and
- use only the `feature/first-person-documentation` branch.

---

### Task 1: Rewrite the README and technical debrief

**Files:**
- Modify: `README.md`
- Modify: `docs/debrief.md`

**Interfaces:**
- Consumes: The approved voice rules in `docs/superpowers/specs/2026-08-14-first-person-documentation-design.md`.
- Produces: The public overview, reflection answers, AI disclosure, and interview narrative in first-person singular.

- [ ] **Step 1: Snapshot protected content for all 16 source documents**

Run this exact command before any source-document rewrite:

```bash
python3 - <<'PY'
import hashlib
import json
import pathlib
import re

root = pathlib.Path('.')
files = [root / 'README.md', *sorted((root / 'docs').rglob('*.md'))]
files = [p for p in files if 'docs/superpowers/' not in p.as_posix()]
fence = re.compile(r'^```.*?^```\s*$', re.MULTILINE | re.DOTALL)
questions = re.compile(r'^### [1-8]\. .+$', re.MULTILINE)

snapshot = {}
for path in files:
    text = path.read_text()
    snapshot[path.as_posix()] = {
        'fences': [hashlib.sha256(block.encode()).hexdigest()
                   for block in fence.findall(text)],
        'reflection_questions': questions.findall(text),
    }

out = pathlib.Path('/tmp/work-management-api-first-person-protected.json')
out.write_text(json.dumps(snapshot, indent=2, sort_keys=True) + '\n')
print(f'wrote {out} for {len(snapshot)} files')
PY
```

Expected: `wrote /tmp/work-management-api-first-person-protected.json for 16 files`.

- [ ] **Step 2: Rewrite `README.md` authorial prose**

Apply these exact semantic rules:

- Rewrite purpose, scenario interpretation, assumptions, architecture-choice rationale, guarantees, limitations, security choices, known debt, production roadmap, reflections, and AI disclosure as submitter decisions or work performed.
- Keep capability lists, authorization matrices, lifecycle diagrams, repository trees, data-model facts, commands, API examples, provider configuration, and runtime-component behavior factual.
- Preserve the eight required reflection-question headings verbatim.
- Preserve the distinction between Mailpit SMTP acceptance and external inbox delivery.

Representative target voice:

```markdown
I built this repository as the backend source of truth for work items.
I chose PostgreSQL and a transactional outbox so assignment state, history,
and notification intent commit atomically.
The worker still provides at-least-once delivery rather than exactly-once
delivery because an SMTP disconnect can occur after provider acceptance.
```

- [ ] **Step 3: Rewrite `docs/debrief.md` as the submitter's spoken explanation**

Use one concise first-person marker for submitter reasoning and actions, with forms such as `I chose`, `I designed`, `I enforce`, `I verify`, `I would add`, and `I can demonstrate`. Keep manager/worker/API/database/SMTP behavior assigned to those actors, and leave fenced demo commands unchanged.

- [ ] **Step 4: Verify Task 1 voice and protected content**

Run:

```bash
git diff --check -- README.md docs/debrief.md
rg -n -i '\b(we|our|ours|ourselves)\b' README.md docs/debrief.md
git diff --word-diff=porcelain -- README.md docs/debrief.md
```

Expected: no whitespace errors; no editorial first-person plural; every prose change is an ownership/voice change rather than a technical claim change.

- [ ] **Step 5: Commit Task 1**

```bash
git add README.md docs/debrief.md
git commit -m "docs: speak in first person in overview"
```

---

### Task 2: Rewrite architecture, operations, and ADR rationale

**Files:**
- Modify: `docs/architecture.md`
- Modify: `docs/operations.md`
- Modify: `docs/adr/0001-modular-monolith.md`
- Modify: `docs/adr/0002-transactional-outbox.md`
- Modify: `docs/adr/0003-identity-and-authorization.md`
- Modify: `docs/adr/0004-lifecycle-concurrency-pagination.md`
- Modify: `docs/adr/0005-libpq-assessment-adapter.md`
- Modify: `docs/adr/0006-pgx-stdlib-driver.md`
- Modify: `docs/adr/0007-private-operational-endpoints.md`

**Interfaces:**
- Consumes: The public voice established in Task 1.
- Produces: First-person architectural rationale and operational ownership while retaining objective component behavior and ADR history.

- [ ] **Step 1: Rewrite architecture ownership prose**

In `docs/architecture.md`, use first person for submitter-selected boundaries and trade-offs. Keep Mermaid diagrams, component names, data flow, transaction ordering, and query ownership statements literal and factual.

- [ ] **Step 2: Rewrite operations ownership prose**

In `docs/operations.md`, use first person for submitter-owned observability controls, failure handling, runbooks, SLO candidates, and planned production work. Keep operator commands imperative, metrics names exact, and failure-mode tables factual.

- [ ] **Step 3: Rewrite ADR context and consequences**

For ADRs 0001–0007:

- Keep titles, status, date, supersession metadata, headings, and links unchanged.
- Express the choice and rationale with concise ownership markers such as `I chose`, `I rejected`, `I accepted`, and `I will revisit` where the submitter is the actor.
- Keep normative runtime behavior and technical imperatives assigned to their real components.
- Keep ADR 0005 explicitly historical and superseded by ADR 0006.

- [ ] **Step 4: Verify Task 2 voice, ADR history, and diagrams**

Run:

```bash
git diff --check -- docs/architecture.md docs/operations.md docs/adr
rg -n -i '\b(we|our|ours|ourselves)\b' docs/architecture.md docs/operations.md docs/adr
rg -n '^Status:|^Date:|^Superseded by:|^Supersedes:' docs/adr
git diff --word-diff=porcelain -- docs/architecture.md docs/operations.md docs/adr
```

Expected: no whitespace errors or editorial plural; ADR metadata and supersession remain intact; diagrams and commands show no content changes.

- [ ] **Step 5: Commit Task 2**

```bash
git add docs/architecture.md docs/operations.md docs/adr
git commit -m "docs: own architecture and operations decisions"
```

---

### Task 3: Rewrite assessment interpretation, plan, review, and evidence narrative

**Files:**
- Modify: `docs/assessment/ambiguities.md`
- Modify: `docs/assessment/architecture-alternatives.md`
- Modify: `docs/assessment/submission-hardening-plan.md`
- Modify: `docs/assessment/self-review.md`
- Modify: `docs/assessment/verification.md`

**Interfaces:**
- Consumes: The selective first-person rules and the protected-content snapshot from Task 1.
- Produces: A first-person assessment record without changing requirements, chronology, or observed evidence.

- [ ] **Step 1: Rewrite interpretation and alternatives prose**

Use first person for submitter-owned assumptions, resolved questions, evaluated alternatives, and design selection. Keep ambiguity and alternatives tables unchanged because they are compact requirement records.

- [ ] **Step 2: Rewrite the hardening plan as completed ownership**

Use first person for the objective, accepted decisions, work sequence, gates, and selected scope. Preserve exact baseline/final commits, dates, branch names, commands, and release-gate meanings. Do not make historical unfinished items sound currently open.

- [ ] **Step 3: Rewrite self-review prose**

Use first person for submitter-owned review, implementation, resolution, acceptance, and verification. Keep requirement-traceability tables, exact findings, limitations, and references unchanged.

- [ ] **Step 4: Rewrite verification lead-ins only**

In `docs/assessment/verification.md`, coordinate narrative sentences under concise ownership markers such as `I ran`, `I observed`, `I reproduced`, `I fixed`, and `I reran`. Preserve every heading containing PASS/FAIL, every fenced command/output block, every exact count/hash/date/path, and the chronological distinction between failing baselines and passing fixes.

- [ ] **Step 5: Verify Task 3 chronology and evidence**

Run:

```bash
git diff --check -- docs/assessment
rg -n -i '\b(we|our|ours|ourselves)\b' docs/assessment
rg -n '^## (PASS|FAIL)' docs/assessment/verification.md
git diff --word-diff=porcelain -- docs/assessment
```

Expected: no whitespace errors or editorial plural; PASS/FAIL headings, evidence blocks, exact identifiers, and requirement tables retain their original meaning.

- [ ] **Step 6: Commit Task 3**

```bash
git add docs/assessment
git commit -m "docs: narrate assessment evidence in first person"
```

---

### Task 4: Run the whole-document voice and protected-content authority

**Files:**
- Modify: `docs/superpowers/specs/2026-08-14-first-person-documentation-design.md`
- Verify: all 16 source documentation files

**Interfaces:**
- Consumes: All rewritten documentation and `/tmp/work-management-api-first-person-protected.json`.
- Produces: A verified first-person documentation set and an implemented design status.

- [ ] **Step 1: Compare protected fenced content and reflection questions**

Run:

```bash
python3 - <<'PY'
import hashlib
import json
import pathlib
import re
import sys

baseline = json.loads(pathlib.Path(
    '/tmp/work-management-api-first-person-protected.json').read_text())
fence = re.compile(r'^```.*?^```\s*$', re.MULTILINE | re.DOTALL)
questions = re.compile(r'^### [1-8]\. .+$', re.MULTILINE)

current = {}
for name in sorted(baseline):
    text = pathlib.Path(name).read_text()
    current[name] = {
        'fences': [hashlib.sha256(block.encode()).hexdigest()
                   for block in fence.findall(text)],
        'reflection_questions': questions.findall(text),
    }

if current != baseline:
    for name in baseline:
        if current[name] != baseline[name]:
            print(f'protected content changed: {name}', file=sys.stderr)
    raise SystemExit(1)
print(f'protected content unchanged across {len(current)} files')
PY
```

Expected: `protected content unchanged across 16 files`.

- [ ] **Step 2: Audit first-person voice without changing real actors**

Run:

```bash
rg -n -i '\b(we|our|ours|ourselves)\b' README.md docs \
  --glob '*.md' --glob '!docs/superpowers/**'
rg -n -i '\b(the implementation|this implementation|the selected design|the project chose|the repository chose)\b' \
  README.md docs --glob '*.md' --glob '!docs/superpowers/**'
```

Expected: no editorial `we/our`; every remaining impersonal match has an actual technical subject rather than a submitter-owned action. Review every match manually instead of forcing a zero-match mechanical replacement.

- [ ] **Step 3: Run repository documentation and static gates**

Run:

```bash
git diff --check
make verify-full
```

Expected: both commands exit 0. The authoritative verifier must report zero
skips in the normal and race PostgreSQL suites, pass the static, contract,
image, Compose, Mailpit, cleanup, and final cleanliness gates, and leave no
project resources behind.

- [ ] **Step 4: Review the complete documentation diff**

Run:

```bash
git diff --stat 91aebe3f9feeacbb5e2421c6d713846d6bb14420...HEAD
git diff --word-diff=porcelain 91aebe3f9feeacbb5e2421c6d713846d6bb14420...HEAD -- \
  README.md docs
git diff --name-only 91aebe3f9feeacbb5e2421c6d713846d6bb14420...HEAD
```

Expected: only Markdown documentation appears; every source-document change is a voice rewrite; no executable or contract file changes.

- [ ] **Step 5: Mark the design implemented and commit final verification metadata**

Change the design status from `Approved for planning` to `Implemented`, then run:

```bash
git add docs/superpowers/specs/2026-08-14-first-person-documentation-design.md
git diff --cached --check
git commit -m "docs: complete first-person voice verification"
git status --short
```

Expected: the commit succeeds and `git status --short` is empty.
