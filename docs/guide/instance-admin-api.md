# Instance admin API

Instance admins manage the whole instance through `/api/private/admin/*`. The routes below can be called with an admin browser session, and, except where noted, with the admin API key. The full request and response shapes are in `internal/api/openapi.yaml`.

## Admin API key

Set `SUPERMETRY_ADMIN_API_KEY` to a random string of at least 32 characters. Shorter values leave key auth off and log a warning at startup. Send it as a bearer token:

```sh
curl -X POST https://analytics.example.test/api/private/admin/users \
  -H "Authorization: Bearer $SUPERMETRY_ADMIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"email":"new.user@example.test","org_name":"Acme","send_reset":true}'
```

The key authenticates `/api/private/admin/*` only. It acts as a hidden system user, shown in the audit log as `admin-key@system.invalid`. It cannot call `update/apply`, which needs the admin's password. Keep it out of shared shells and rotate it by changing the environment and restarting.

## Delete an organization

`DELETE /api/private/admin/organizations/{org}` deletes an organization the same way an owner delete does: it can be restored for 7 days and the notice emails are sent. Organizations that hold only demo data, or nothing, are erased at once.

## Set site retention

`PATCH /api/private/admin/sites/{site_id}` with `{"retention_days": 30}` sets how long raw data is kept. Allowed values are `0`, `7`, `30`, `90` and `365`.

## Create a user

`POST /api/private/admin/users` with `email`, and optionally `name`, `org_name` and `send_reset`. The user gets an unusable password; with `org_name` they own a new organization. It returns `201`, or `409` if the email exists. With `send_reset` the user is emailed a password-set link; when mail is not configured the response carries a one-time `reset_url` instead.

## Sign in as a user

`POST /api/private/admin/users/{user}/impersonate` starts a support session as that user.

- With a browser session, the admin must re-enter their password. The admin's own session is replaced and the call returns `204`.
- With the admin key, it returns `200` with `{token, expires_at}`.

The session lasts 1 hour (a hard limit) and the UI shows a banner. It cannot use admin routes, account-security actions, invitations, member role changes, share links or API keys. It is refused for instance admins (`403`), disabled users (`403`) and users who must reset their password (`409`). Each start is recorded in the instance audit and in each of the user's organizations, and the session ends if the admin is demoted, disabled or deleted.
