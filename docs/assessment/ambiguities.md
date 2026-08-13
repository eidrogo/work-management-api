# Phase 1 — Interpretation and Ambiguity Check

| Topic | Ambiguity | Chosen interpretation | Rationale | Risk if wrong | Reversal cost | Stakeholder validation needed |
|---|---|---|---|---|---|---|
| Work-item creation | Who may create work | Managers only | Managers define the work in the scenario | Workers may need an intake path | Low: add a separate worker command | Yes |
| Worker creation | Whether workers may create | No in v1 | Keeps roles and authorization unambiguous | Worker-originated requests need another channel | Low | Yes |
| Manager editing | Whether managers may edit after assignment | No general edit endpoint | Not required for the core flow; avoids mutability ambiguity | Typos require an administrative correction path | Medium | Yes |
| Manager progression | Whether a manager may start or complete | Never | Explicit business prohibition | Emergency recovery needs a later operator flow | Low | No |
| Initial state | State on creation | `UNASSIGNED` | Work may exist before ownership | Some clients may want create-and-assign | Low | No |
| Lifecycle | Allowed states | `UNASSIGNED → ASSIGNED → IN_PROGRESS → COMPLETED` | Smallest coherent forward flow | Cancellation/reopen may be needed | Medium | Yes |
| Backward movement | Whether state may regress | No | Preserves clarity and auditability | Mistakes need an administrative recovery process | Medium | Yes |
| Reassignment | When a manager may reassign | Only while `ASSIGNED` | Avoids silently transferring active work | Operations may need mid-work transfer | Medium | Yes |
| Reassignment reason | Whether reason is required | Required when assignee changes | Preserves accountability | Adds client burden | Low | Yes |
| Unassignment | Whether assignment may be removed | Not implemented | It is a backward lifecycle operation | Erroneous assignment cannot be undone directly | Medium | Yes |
| Completed mutability | Whether completed work changes | Immutable | Explicit recommended invariant | Corrections need a future reopen/correction command | Medium | Yes |
| Cancellation/deletion | Whether supported | Neither in v1 | Outside the core scenario | Stale items remain visible | Medium | Yes |
| Authentication boundary | Who authenticates users | External system in production | Matches the scenario | Production issuer contract is not known | Low at adapter boundary | Yes |
| Local authentication | Reviewer mechanism | Seeded opaque bearer tokens stored only as SHA-256 hashes | Demonstrates a credible boundary without building identity | Development tokens are long-lived | Low | No |
| Authorization location | HTTP handler or application layer | Application service below transport | Prevents a future transport from bypassing policy | None material | Low | No |
| Delivery guarantee | Meaning of assignment notification guarantee | Assignment and notification intent commit atomically; delivery is asynchronous and at least once | Prevents lost intent without overstating SMTP | Small duplicate window | Medium | No |
| `SENT` meaning | Inbox delivery or provider acceptance | SMTP server accepted the message after `DATA` | This is the strongest generic SMTP boundary available | Bounce or spam rejection may follow | Low | No |
| Retry behavior | Unlimited or bounded | Bounded exponential equal-jitter retry per delivery cycle, with attributed manual replay capped at delivery cycle 10 | Operable and finite over the delivery lifetime | Provider-specific policy may differ | Low | Yes |
| Duplicate email | Behavior after ambiguous provider response | Duplicate is possible after SMTP acceptance and before DB update | Generic SMTP has no idempotency key | Recipient may see a duplicate | Medium | No |
| Notification failure | Whether assignment rolls back | No; assignment remains committed and delivery remains visible/retryable | Ownership must not disappear because SMTP is down | Worker may learn late | Low | No |
| Audit scope | Full audit or core mutation history | Create, assignment/reassignment, progress, completion, every delivery attempt, and every attributed replay | Covers the required core and operator resend decisions | Broader operator/configuration audit is deferred | Medium | Yes |
| Pagination | Offset or cursor | Keyset cursor | Stable and proportional to page size | No arbitrary page jumps | Medium | No |
| Mutation idempotency | Retry contract | Required create key with an immutable first-response snapshot; deterministic no-op semantics for repeated assignment/target state; version precondition otherwise | Replays remain stable after later row changes and timeout retries do not duplicate effects | Clients must retain command metadata | Medium | Yes |
| Concurrent updates | Last-write-wins or explicit conflict | Optimistic work-item version; one-row `SKIP LOCKED` claims using database time and a fresh UUID token fenced by owner/attempt/unexpired lease | Explicit and testable across competing clients and workers | Hot items produce client-visible conflicts | Medium | No |
| Multi-team scope | Single or multi-tenant | Single team in v1 | Multi-team is a future direction | Adding `team_id` later is a significant migration | High | Yes before production |
| Implemented versus deferred | Breadth expected in assessment | Implement the transactional core, real transport, operability, docs, and tests; defer OIDC, webhooks, tenant isolation, delivery webhooks, rate limits, and cloud infrastructure | Preserves depth | Roadmap work remains | Low | No |

**BLOCKING AMBIGUITIES: NONE**

The stakeholder questions are important, but none prevents a coherent and reversible v1. The public contract deliberately avoids expensive speculative commitments such as configurable workflows or multi-tenancy.
