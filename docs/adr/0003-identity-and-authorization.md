# ADR 0003: External Identity Boundary with Application-Layer Authorization

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

I kept authentication outside the business system but required a credible boundary for local reviewers and policy that future transports cannot bypass.

## Decision

I chose to resolve opaque development bearer tokens to immutable principals, store only token hashes, and enforce manager/worker and ownership policy in application services below HTTP handlers.

## Alternatives considered

I rejected:

- caller-controlled identity headers because they create an unsafe trust boundary;
- issuing JWTs locally because that builds an identity platform outside scope; and
- handler-only authorization because another transport could bypass it.

## Consequences

I accepted development tokens as intentionally non-production. OIDC/JWT verification can replace the authenticator without moving business policy.

## Operational impact

The service counts authentication and authorization failures without logging credentials. In production, I will validate issuer, audience, signature, expiry, and key rotation.

## Revisit when

I will revisit this when roles become fine-grained permissions or multi-team isolation introduces tenant claims and optional row-level security.
