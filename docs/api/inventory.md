# API route inventory

| Measure | Total |
|---|---:|
| Registered routes | 251 |
| Documented routes | 139 |
| Routes hit by tests | 249 |

## Routes not hit by tests

### `/api/replay/chunk`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| OPTIONS | `/api/replay/chunk` | `deps.Replay` | none |

### `/api/replay/start`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| OPTIONS | `/api/replay/start` | `deps.Replay` | none |

## Routes missing from OpenAPI

### `/`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| * | `/` | `func` | none |

### `/api/private/`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| * | `/api/private/` | `func` | session |

### `/api/private/annotations`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| DELETE | `/api/private/annotations/{annotation}` | `h.deleteAnnotation` | session |
| PATCH | `/api/private/annotations/{annotation}` | `h.updateAnnotation` | session |

### `/api/private/auth`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/private/auth/config` | `h.authConfig` | none |
| POST | `/api/private/auth/login` | `h.login` | none |
| POST | `/api/private/auth/logout` | `h.logout` | none |
| POST | `/api/private/auth/password-reset` | `h.startPasswordReset` | none |
| POST | `/api/private/auth/password-reset/confirm` | `h.finishPasswordReset` | none |
| POST | `/api/private/auth/signup` | `h.signup` | none |

### `/api/private/funnels`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| POST | `/api/private/funnels/query` | `h.funnelRequest` | session |
| DELETE | `/api/private/funnels/{funnel}` | `h.deleteFunnel` | session |
| PATCH | `/api/private/funnels/{funnel}` | `h.updateFunnel` | session |

### `/api/private/goals`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| DELETE | `/api/private/goals/{goal}` | `h.deleteGoal` | session |
| PATCH | `/api/private/goals/{goal}` | `h.updateGoal` | session |

### `/api/private/health`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/private/health` | `dataHealthHandler` | session |

### `/api/private/heatmaps`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| POST | `/api/private/heatmaps/query` | `h.heatmapRequest` | session |

### `/api/private/imports`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| DELETE | `/api/private/imports/{import}` | `h.deleteImport` | session |

### `/api/private/invitations`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| POST | `/api/private/invitations/{token}/accept` | `h.acceptInvitation` | none |

### `/api/private/journeys`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| POST | `/api/private/journeys/query` | `h.journeyRequest` | session |

### `/api/private/links`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| DELETE | `/api/private/links/{link}` | `h.deleteLink` | session |
| GET | `/api/private/links/{link}` | `h.getLink` | session |
| PATCH | `/api/private/links/{link}` | `h.updateLink` | session |
| GET | `/api/private/links/{link}/stats` | `h.linkStats` | session |

### `/api/private/me`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/private/me/ip` | `h.currentIP` | session |
| DELETE | `/api/private/me/sessions` | `h.revokeOtherSessions` | session |
| GET | `/api/private/me/sessions` | `h.listSessions` | session |
| DELETE | `/api/private/me/sessions/{session}` | `h.revokeSession` | session |
| DELETE | `/api/private/me/totp` | `h.disableTOTP` | session |
| PATCH | `/api/private/me/totp` | `h.confirmTOTP` | session |
| POST | `/api/private/me/totp` | `h.enrollTOTP` | session |
| POST | `/api/private/me/totp/recovery-codes` | `h.regenerateTOTPRecoveryCodes` | session |

### `/api/private/orgs`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/private/orgs` | `h.listOrgs` | session |
| POST | `/api/private/orgs` | `h.createOrg` | session |
| GET | `/api/private/orgs/{org}` | `h.getOrg` | session |
| GET | `/api/private/orgs/{org}/api-keys` | `h.listAPIKeys` | session |
| POST | `/api/private/orgs/{org}/api-keys` | `h.createAPIKey` | session |
| DELETE | `/api/private/orgs/{org}/api-keys/{key}` | `h.revokeAPIKey` | session |
| GET | `/api/private/orgs/{org}/audit` | `h.listAudit` | session |
| GET | `/api/private/orgs/{org}/channels` | `h.listChannels` | session |
| POST | `/api/private/orgs/{org}/channels` | `h.createChannel` | session |
| DELETE | `/api/private/orgs/{org}/channels/{channel}` | `h.deleteChannel` | session |
| PATCH | `/api/private/orgs/{org}/channels/{channel}` | `h.updateChannel` | session |
| POST | `/api/private/orgs/{org}/channels/{channel}/test` | `h.testChannel` | session |
| GET | `/api/private/orgs/{org}/invitations` | `h.listInvitations` | session |
| POST | `/api/private/orgs/{org}/invitations` | `h.createInvitation` | session |
| DELETE | `/api/private/orgs/{org}/invitations/{invitation}` | `h.revokeInvitation` | session |
| GET | `/api/private/orgs/{org}/members` | `h.listMembers` | session |
| DELETE | `/api/private/orgs/{org}/members/{user}` | `h.deleteMember` | session |
| PATCH | `/api/private/orgs/{org}/members/{user}` | `h.updateMember` | session |

### `/api/private/profiles`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/private/profiles/{profile}` | `h.getProfile` | session |

### `/api/private/query`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| POST | `/api/private/query` | `h.queryRequest` | session |

### `/api/private/realtime`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/private/realtime` | `realtimeHandler` | session |
| GET | `/api/private/realtime/summary` | `realtimeSummaryHandler` | session |

### `/api/private/retention`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| POST | `/api/private/retention/query` | `h.retentionRequest` | session |

### `/api/private/segments`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| DELETE | `/api/private/segments/{segment}` | `h.deleteSegment` | session |
| PATCH | `/api/private/segments/{segment}` | `h.updateSegment` | session |

### `/api/private/sessions`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/private/sessions/{session}` | `h.getSession` | session |

### `/api/private/setup`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| POST | `/api/private/setup` | `h.setup` | none |

### `/api/private/sites`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/private/sites` | `h.listSites` | session |
| POST | `/api/private/sites` | `h.createSite` | session |
| GET | `/api/private/sites/{site}` | `h.getSite` | session |
| PATCH | `/api/private/sites/{site}` | `h.updateSite` | session |
| GET | `/api/private/sites/{site}/alerts` | `h.listAlerts` | session |
| POST | `/api/private/sites/{site}/alerts` | `h.createAlert` | session |
| DELETE | `/api/private/sites/{site}/alerts/{alert}` | `h.deleteAlert` | session |
| PATCH | `/api/private/sites/{site}/alerts/{alert}` | `h.updateAlert` | session |
| GET | `/api/private/sites/{site}/annotations` | `h.listAnnotations` | session |
| POST | `/api/private/sites/{site}/annotations` | `h.createAnnotation` | session |
| GET | `/api/private/sites/{site}/events` | `h.events` | session |
| GET | `/api/private/sites/{site}/export` | `h.exportSite` | session |
| GET | `/api/private/sites/{site}/funnels` | `h.listFunnels` | session |
| POST | `/api/private/sites/{site}/funnels` | `h.createFunnel` | session |
| GET | `/api/private/sites/{site}/goals` | `h.listGoals` | session |
| POST | `/api/private/sites/{site}/goals` | `h.createGoal` | session |
| POST | `/api/private/sites/{site}/icon/refetch` | `h.refetchSiteIcon` | session |
| GET | `/api/private/sites/{site}/imports` | `h.listImports` | session |
| POST | `/api/private/sites/{site}/imports` | `h.createImport` | session |
| POST | `/api/private/sites/{site}/imports/preview` | `h.previewImport` | session |
| GET | `/api/private/sites/{site}/links` | `h.listLinks` | session |
| POST | `/api/private/sites/{site}/links` | `h.createLink` | session |
| GET | `/api/private/sites/{site}/pixel` | `h.pixelSnippet` | session |
| GET | `/api/private/sites/{site}/pixel/stats` | `h.pixelStats` | session |
| GET | `/api/private/sites/{site}/profiles` | `h.listProfiles` | session |
| GET | `/api/private/sites/{site}/properties` | `h.properties` | session |
| GET | `/api/private/sites/{site}/properties/{key}/values` | `h.propertyValues` | session |
| GET | `/api/private/sites/{site}/purge-requests` | `h.listDataPurges` | session |
| POST | `/api/private/sites/{site}/purge-requests` | `h.createDataPurge` | session |
| GET | `/api/private/sites/{site}/reports` | `h.listReports` | session |
| POST | `/api/private/sites/{site}/reports` | `h.subscribeReport` | session |
| DELETE | `/api/private/sites/{site}/reports/{report}` | `h.deleteReport` | session |
| GET | `/api/private/sites/{site}/segments` | `h.listSegments` | session |
| POST | `/api/private/sites/{site}/segments` | `h.createSegment` | session |
| GET | `/api/private/sites/{site}/sessions` | `h.listSessions` | session |
| GET | `/api/private/sites/{site}/snippet` | `h.siteSnippet` | session |
| GET | `/api/private/sites/{site}/suggestions` | `h.suggestions` | session |

### `/api/private/stats`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/private/stats/breakdown` | `h.statsBreakdown` | session |
| GET | `/api/private/stats/summary` | `h.statsSummary` | session |
| GET | `/api/private/stats/timeseries` | `h.statsTimeseries` | session |

### `/api/private/vitals`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| POST | `/api/private/vitals/query` | `h.vitalsRequest` | session |

### `/api/public/openapi.json`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/api/public/openapi.json` | `serveOpenAPI` | none |

### `/api/public/v1`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| * | `/api/public/v1/` | `func` | public-key |

### `/api/replay/chunk`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| OPTIONS | `/api/replay/chunk` | `deps.Replay` | none |
| POST | `/api/replay/chunk` | `deps.Replay` | none |

### `/api/replay/css`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| OPTIONS | `/api/replay/css/{hash}` | `deps.ReplayCSS` | none |
| PUT | `/api/replay/css/{hash}` | `deps.ReplayCSS` | none |

### `/api/replay/start`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| OPTIONS | `/api/replay/start` | `deps.Replay` | none |
| POST | `/api/replay/start` | `deps.Replay` | none |

### `/js/`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/js/` | `tracker.SiteHandlerWithPolicy` | none |

### `/js/script.full.js`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/js/script.full.js` | `tracker.FullHandler` | none |

### `/js/script.js`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/js/script.js` | `tracker.Handler` | none |

### `/replay.js`

| Method | Pattern | Handler | Auth |
|---|---|---|---|
| GET | `/replay.js` | `func` | none |

## OpenAPI operations with no route

None.

## Unknown auth derivation

None.
