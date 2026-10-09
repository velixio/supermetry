# Event attribution in closed analytics

Custom-event rows now retain the attribution carried by each raw event when a day is compacted. Session acquisition fields remain unchanged and continue to answer session and acquisition reports.

The closed representation distinguishes three states:

- A nonzero event attribution ID is the value observed on the event.
- Zero means the event was observed without that attribution value. It is not replaced with the session value.
- `NULL` means the closed row predates event-attribution preservation and no event-level evidence is available.

Queries that combine custom events or revenue with `source`, `channel`, `utm_source`, `utm_medium`, or `utm_campaign` use event attribution. Closed rows with any unknown event-attribution field are excluded from those queries. They remain available to custom-event and revenue queries that do not request or filter by event attribution. This prevents legacy session attribution from being presented as event attribution.

Responses declare their attribution basis and coverage in `meta.event_attribution`. Hot-only results report `basis: event`, `coverage: exact`, and `legacy_excluded: false`. Results with a closed component report `coverage: available_only` and `legacy_excluded: true`, including mixed hot/closed ranges. Comparison results carry the same metadata for their own range.

## Rollout and rollback boundary

Migration 0029 only adds nullable columns, so applying it does not rewrite historical rows. New or reclosed days populate the columns from retained raw events. Historical rows can be recovered by closing the day again only while those raw events still exist; after raw expiry, unavailable attribution cannot be reconstructed.

The migration and the new closer must be rolled out together before relying on event-level closed reports. An older binary can still run against the additive schema, but closing or reclosing a day with that binary writes `NULL` event attribution and discards this evidence from the closed representation. Rolling back the closer is therefore not data-safe for event-attribution reporting. Avoid concurrent old and new closers during rollout, and do not roll back after raw expiry without accepting reduced coverage.

No database work was added to ingestion. Attribution is copied by the existing day-close operation, and query filtering happens on the analytical read path.
