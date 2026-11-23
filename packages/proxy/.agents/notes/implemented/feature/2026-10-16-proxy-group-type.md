# Agent Note: Proxy groups carry an explicit type

Status: implemented

## Problem

Residential channel groups and subscription groups are both proxy groups, but they
are different products: a residential group's exits belong to a channel (sessions,
leases, rotation through `/ctl/`), while a subscription group selects among nodes
imported from subscriptions. `GET /api/v1/proxy-groups` returned both with no field
saying which is which, so API and SDK callers could neither tell them apart nor
filter, and had to guess from `source_spec` (a residential group's spec looks like any
custom group's).

## Decision

Every group in the API carries `type`: `residential` when a residential channel owns
it (`residential_channels.proxy_group_id`), `subscription` when its spec references
subscriptions, otherwise `custom` (explicit nodes, group compositions, DIRECT, pinned
listener anchors). The type is derived at read time in `internal/proxygroup/group_type.go`;
ownership wins over the spec. `GET /api/v1/proxy-groups?type=residential` (or a
comma-separated list) filters, and an unknown value is a 422 rather than an ignored
filter. Subscription-only features (Retain) refuse non-subscription groups.

## Alternatives considered

- **Leave it to callers** (status quo): no schema change, but every caller must
  re-derive ownership from the residential channel list, and nothing stops an SDK from
  mixing the two in one picker; the operator ruled that out.
- **Store a type column on proxy_groups**: a second source of truth that can drift from
  channel ownership; deriving from the owning row cannot.
- **Two separate endpoints**: breaks existing callers of `/api/v1/proxy-groups` and
  composition (`group_ids`) still spans both kinds.

## Consequences

`type` is a read-only field; create and update ignore nothing new because the field is
not part of the request types. A group created by a residential channel reads as
`custom` only in the instant before the channel row links it.

## Testing

internal/proxygroup/group_type_test.go (residential by ownership, custom, subscription,
filter parsing) and internal/api/proxy_group_type_test.go (field present, filter,
422 on unknown values).
