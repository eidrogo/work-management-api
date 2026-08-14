# Reduce First-Person Repetition

**Status:** Approved
**Date:** 2026-08-14

## Goal

Keep the documentation personal without starting most author-owned statements with `I`. The approved baseline is 332 matching lines with first-person singular terms; the rewrite should make that voice occasional rather than dominant.

## Chosen approach

Use a balanced technical voice:

- Keep first person for explicit ownership, judgment, accountability, and AI disclosure.
- Prefer the actual subject elsewhere: the API, worker, design, repository, test suite, or decision.
- Use concise labels and direct constructions in reflections, such as `Audit: extend...` instead of `For audit, I would extend...`.
- Combine adjacent sentences when they repeat the same first-person subject.

Two alternatives were rejected:

1. Mechanical pronoun removal would produce passive or unnatural prose.
2. Removing first person completely would weaken ownership and make the AI disclosure impersonal.

## Scope

Apply the rewrite to every tracked Markdown file. Do not delete files, sections, requirements, evidence, commands, diagrams, tables, links, or technical detail.

Protected content remains byte-identical:

- fenced code, command output, JSON, SQL, and diagrams;
- assessment questions and structured tables;
- ADR metadata, dates, statuses, and supersession history;
- hashes, scan IDs, commit IDs, PASS/FAIL chronology, and exact verification claims;
- API names, routes, error codes, configuration names, and runtime actor attribution.

## Style rules

- Use `I` or `my` only when the submitter is genuinely the important subject.
- Avoid repeated `I would`, `I chose`, `I implemented`, `I verified`, and `I used` openings.
- Prefer active technical subjects over passive voice.
- Preserve first-person answers where removing ownership would sound evasive.
- Do not strengthen claims, change tense, or convert future work into completed work.

## Success criteria

1. First-person singular matching lines fall by at least 50% from the 332-line baseline.
2. No paragraph reads like a mechanical pronoun substitution.
3. All protected content remains unchanged.
4. Every required assessment answer and technical claim remains present.
5. Markdown links, repository checks, and `make verify-full` pass.
6. An independent review finds no semantic drift or unnatural voice.
