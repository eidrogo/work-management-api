# ADR 0003: External Identity Boundary with Application-Layer Authorization

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

I kept authentication outside the business system, but I needed a credible boundary for local reviewers and policy that future transports cannot bypass.

## Decision

I chose to resolve opaque development bearer tokens to immutable principals, store only token hashes, and enforce manager/worker and ownership policy in application services below HTTP handlers.

## Alternatives considered

- I rejected caller-controlled identity headers because they create an unsafe trust boundary.
- I rejected issuing JWTs locally because that builds an identity platform outside scope.
- I rejected handler-only authorization because another transport could bypass it.

## Consequences

I accepted development tokens as intentionally non-production. I can replace the authenticator with OIDC/JWT verification without moving business policy.

## Operational impact

I count authentication and authorization failures without logging credentials. In production, I will validate issuer, audience, signature, expiry, and key rotation.

## Revisit when

I will revisit this when roles become fine-grained permissions or multi-team isolation introduces tenant claims and optional row-level security.
