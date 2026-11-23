# Agent Note: Subscription and residential contracts are two OpenAPI documents

Status: implemented

## Problem

SDKs and agents need a machine-readable contract, but the token-addressed surface
mixes two different resources: a subscription proxy service (read-only node export
via `/nodes/` and `/sub/`) and a residential channel (a fixed pool of logical nodes
whose exits are rotated under leases and an allocation-version CAS via `/ctl/`). The
operator requires the two to stay completely separate, especially in APIs and SDKs.

## Decision

`docs/openapi/subscription-consumer.openapi.yaml` and
`docs/openapi/residential-control.openapi.yaml` are separate documents with disjoint
paths and schemas. `internal/api/openapi_test.go` checks each schema's properties
recursively against the Go types that render the responses (`listener.ConsumerPayload`,
`residential.ControlNodeList`, `residential.ControlNode` and their nested types), the
`/sub` media types against the renderer,
and that neither document exposes the other's paths. The prose contracts remain the
source of truth for semantics; the OpenAPI documents are their machine-readable form.

## Alternatives considered

- **Prose contracts only** (status quo): one source of truth and nothing to keep in
  sync, but SDK generators and agents cannot consume them, and nothing mechanical
  stops the two resources from being described together.

- **One OpenAPI document with tags**: one SDK generated from it would put leases and
  rotation next to read-only service exports, which is exactly the mixing the operator
  ruled out.
- **Generating OpenAPI from Go types**: needs a new dependency or reflection layer;
  the drift test gives the same guarantee for the four response types.

## Consequences

`/nodes/{shareToken}` still accepts a residential channel share token for
compatibility; the subscription document says so and directs residential
integrations to `/ctl/`. Admin APIs under `/api/v1/` are not covered here; the
capability catalog remains their machine-readable form.

## Testing

`go test ./internal/api -run OpenAPI`.
