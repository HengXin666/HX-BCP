# Agent Note: Ingress ports are deployment facts, not page controls

Status: implemented

## Problem

Service creation, editing and runtime settings expose editable ingress ports even
though the deployed shared entry points use fixed ports. Changing a member port
cannot change the carrier socket and encourages publishing an unreachable address.

## Decision

Runtime settings and service forms render ingress ports as read-only output, with
no state setters. Existing deployment values are preserved. Shared service forms
derive local/public ports from the loaded family settings; edits retain stored ports
outside shared mode. Shared bindings are read-only in member forms because they
belong to the carrier. Switching protocols selects the matching family's fixed port.

WebSocket ingress uses the configured public port and URL path. Plain HTTP/SOCKS
uses the Mixed socket and authentication username, not an HTTP URL path. These
different protocol semantics are explicit in the Docker help and runtime settings.
SMTP and residential provider gateway ports are upstream service addresses, not
HX ingress ports; their existing configuration is unaffected.

## Alternatives considered

- **Do nothing / rely on backend convergence:** the compiler already knows the
  carrier endpoint, but the UI still promises a port change it cannot fulfill.
- **Force every stored port to 7890/7891/443:** uniform defaults simplify forms,
  but silently break existing deployments and reverse-proxy mappings.
- **Remove port fields from the API:** enforces a stronger restriction, but changes
  deployment/automation contracts unnecessarily; the requested restriction is on
  settings pages, not an on-disk migration or removal of administrative APIs.

## Consequences

Users cannot change HX ingress ports from these pages. Deployment tools can still
configure them, and old per-service records keep their ports. New dedicated-service
forms use family defaults and can encounter a port conflict in historical per-service
mode; shared mode is the supported fixed-entry workflow. Upstream address editors
remain available because HX does not choose those remote ports.

## Testing

Browser regressions inspect runtime settings and create/edit forms on desktop and
mobile, assert that port fields are outputs rather than editable inputs, and capture
screenshots. Frontend type checking verifies there are no dangling port setters.
