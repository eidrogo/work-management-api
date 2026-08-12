# ADR 0003: External Identity Boundary with Application-Layer Authorization

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

Authentication exists outside the business system, but local reviewers need a credible boundary and future transports must not bypass policy.

## Decision

Resolve opaque development bearer tokens to immutable principals. Store only token hashes. Enforce manager/worker and ownership policy in application services below HTTP handlers.

## Alternatives considered

- Caller-controlled identity headers: unsafe trust boundary.
- Issuing JWTs locally: builds an identity platform outside scope.
- Handler-only authorization: another transport could bypass it.

## Consequences

Development tokens are intentionally non-production. OIDC/JWT verification can replace the authenticator without moving business policy.

## Operational impact

Count authentication and authorization failures without logging credentials. Production must validate issuer, audience, signature, expiry, and key rotation.

## Revisit when

Roles become fine-grained permissions or multi-team isolation introduces tenant claims and optional row-level security.
