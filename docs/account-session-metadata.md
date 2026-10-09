# Account session metadata

The account **Active sessions** page shows the signed-in person's login sessions,
not analytics visitor sessions. Each new login or signup records its resolved IP
address, an approximate two-letter country from the server's local GeoIP database,
and a recognized browser name. It never calls an external geolocation service or
stores the raw User-Agent. Browser identification is a client claim, not proof of
a device's identity. IP and country can reflect a VPN or proxy.

Metadata describes sign-in, not a subsequent network/location change. The server
uses the direct peer unless it belongs to configured trusted proxies; in that case
it selects the nearest untrusted forwarded hop, including private addresses.
Invalid addresses, missing GeoIP results, local/private IP locations, oversized
User-Agents and unrecognized browser names display as unknown. Migration 0028
adds nullable columns without backfilling historical sessions or changing their
creation/activity timestamps.

Creation is the actual login-session creation time. Last active is server-observed
authenticated request activity, with writes throttled to once a minute. The known
dashboard polling routes (query, realtime, health, installation status and import
progress) and the session-list read do not refresh it. Manual requests to those
same read endpoints also do not refresh it. Other authenticated requests may
refresh it, so it is not a measurement of human interaction. Both authentication
adapters apply the same rules. Expiry and revocation checks still run for polling.
The UI displays local time and provides the exact UTC instant in accessible text
and the timestamp tooltip.

Access and revocation remain scoped to the authenticated user, regardless of
organization role. The current-session marker is derived server-side from the
presented cookie. Responses do not contain login tokens, CSRF values or raw UAs.
The existing opaque revocation identifier is not a login credential.

## Retention

IP metadata is new account security data, held on the existing `user_sessions`
row only. Absolute expiry remains 90 days and idle expiry remains 14 days. The
existing hourly `user-session-cleanup` job deletes expired/idle rows and their
metadata together, subject to successful scheduler execution; logout, revocation,
account deletion and applicable password resets delete them earlier. There is no
new metadata history or retention period. Existing database-backup retention is
unchanged. Anonymous analytics policy and visitor-IP handling are unchanged.
