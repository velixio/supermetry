# Supermetry Helm chart

Deploys [Supermetry](https://github.com/velixio/supermetry), self-hosted
privacy-first web analytics, on Kubernetes 1.25 or newer. By default it also
runs a small PostgreSQL 18 StatefulSet (official `postgres:18-alpine` image, no
chart dependency), so one command gives a working instance.

## Install

From a checkout of the repository:

```sh
helm install supermetry deploy/helm/supermetry \
  --namespace supermetry --create-namespace \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set 'ingress.hosts[0].host=analytics.example.com' \
  --set 'ingress.hosts[0].paths[0].path=/' \
  --set 'ingress.tls[0].secretName=supermetry-tls' \
  --set 'ingress.tls[0].hosts[0]=analytics.example.com'
```

`SUPERMETRY_BASE_URL` is derived from the first ingress host (https when the
host is listed under `ingress.tls`); set `config.baseUrl` to override it.
Without an ingress it defaults to `http://localhost:8787`, which works with
`kubectl port-forward svc/supermetry 8787:8787`.

On first start Supermetry prints a one-time setup link. Open it to create the
admin account:

```sh
kubectl -n supermetry logs deploy/supermetry | grep 'setup?token='
```

The link is regenerated on every restart until setup is complete. Check the
install with `helm test supermetry -n supermetry`; it requests `/healthz`
through the Service.

## External PostgreSQL

Any PostgreSQL 14 or newer works; 18 is what CI runs. Disable the bundled
database and pass a connection URL, either in values (it is stored in the
chart's Secret):

```yaml
postgresql:
  enabled: false
externalDatabase:
  url: postgres://supermetry:PASSWORD@db.example.com:5432/supermetry?sslmode=require
```

or from an existing Secret:

```sh
kubectl -n supermetry create secret generic supermetry-db \
  --from-literal=database-url='postgres://supermetry:PASSWORD@db.example.com:5432/supermetry?sslmode=require'
helm install supermetry deploy/helm/supermetry -n supermetry \
  --set postgresql.enabled=false \
  --set externalDatabase.existingSecret=supermetry-db
```

Percent-encode special characters in the password inside the URL.

## Secrets

`SUPERMETRY_SECRET_KEY` (base64 of at least 32 bytes) encrypts TOTP and
notification secrets. If `secretKey.value` and `secretKey.existingSecret` are
empty, the chart generates one on first install and reuses it on every upgrade
(via `lookup`). The bundled database password is handled the same way.

The chart's Secret carries `helm.sh/resource-policy: keep`: `helm uninstall`
leaves it, and the bundled database's volume claim, in place, so reinstalling
the same release name over the old data keeps working. Back up the key:

```sh
kubectl -n supermetry get secret supermetry -o jsonpath='{.data.secret-key}' | base64 -d
```

To manage it yourself:

```sh
kubectl -n supermetry create secret generic supermetry-key \
  --from-literal=secret-key="$(openssl rand -base64 32)"
helm install ... --set secretKey.existingSecret=supermetry-key
```

`helm template` and `--dry-run` cannot read the cluster, so they show a fresh
random value each time; real installs and upgrades do not.

## Storage and scaling

| Path | What | Volume |
| --- | --- | --- |
| `/data/supermetry` (`config.dataDir`) | GeoIP downloads (MaxMind), on-disk session replays, replay upload spill | `persistence.*` (5Gi PVC) |
| `/tmp` | Dashboard import uploads | emptyDir |
| `/var/lib/postgresql` (bundled DB) | PostgreSQL 18 cluster | `postgresql.persistence.*` (8Gi PVC) |

The data directory is a subdirectory of the volume because Supermetry chmods it
to `0700`, which fails on a volume root owned by root. Visitor salts, sessions
and all analytics data live in PostgreSQL.

`replicaCount` must stay `1` and there is no HorizontalPodAutoscaler: live
sessions are aggregated in process memory, so a second instance would split
visitors' sessions. The Deployment uses the `Recreate` strategy because the
data volume is `ReadWriteOnce`. Scale vertically with `resources`.

With `persistence.enabled=false` the data directory is an emptyDir; store
replays in S3 (`SUPERMETRY_REPLAY_S3_URL`) in that case.

## Client IPs behind an ingress

Supermetry trusts `X-Forwarded-For` only from addresses in
`SUPERMETRY_TRUSTED_PROXIES` (`config.trustedProxies`). The default trusts
private ranges, which covers in-cluster ingress controllers. The controller
itself must see the real client address, for example with
`externalTrafficPolicy: Local` on its LoadBalancer Service or the PROXY
protocol, and must overwrite `X-Forwarded-For` rather than append untrusted
values. Set `SUPERMETRY_INGEST_DIAG=1` (with `config.env`) temporarily to check
what Supermetry sees.

## Security

The pods satisfy the `restricted` Pod Security Standard: non-root (uid 65532
for the app, 70 for PostgreSQL), read-only root filesystem, no privilege
escalation, all capabilities dropped, `RuntimeDefault` seccomp, and no service
account token.

## Values

See [values.yaml](values.yaml); every key is commented. The most common ones:

| Key | Default | Description |
| --- | --- | --- |
| `image.tag` | chart `appVersion` | Supermetry image tag |
| `config.baseUrl` | derived | `SUPERMETRY_BASE_URL` |
| `config.trustedProxies` | private ranges | `SUPERMETRY_TRUSTED_PROXIES` |
| `config.signup` | `invite` | `SUPERMETRY_SIGNUP` |
| `config.env` | `{}` | Extra `SUPERMETRY_*` settings |
| `extraEnv`, `extraEnvFrom` | `[]` | Extra env vars, ConfigMaps and Secrets (SMTP credentials, S3 URL) |
| `secretKey.value` / `secretKey.existingSecret` | generated | `SUPERMETRY_SECRET_KEY` |
| `postgresql.enabled` | `true` | Run the bundled PostgreSQL 18 |
| `externalDatabase.url` / `externalDatabase.existingSecret` | empty | External database |
| `persistence.size` | `5Gi` | Data volume size |
| `ingress.*` | disabled | Ingress with `className` and TLS |
| `resources` | 100m / 128Mi, 512Mi limit | App resources |

Every other setting is documented in
[docs/guide/configuration.md](../../../docs/guide/configuration.md). For
example, SMTP:

```yaml
config:
  env:
    SUPERMETRY_SMTP_HOST: smtp.example.com
    SUPERMETRY_SMTP_PORT: "587"
    SUPERMETRY_SMTP_USER: supermetry
    SUPERMETRY_SMTP_FROM: analytics@example.com
extraEnv:
  - name: SUPERMETRY_SMTP_PASS
    valueFrom:
      secretKeyRef:
        name: supermetry-smtp
        key: password
```

## Upgrade

```sh
helm upgrade supermetry deploy/helm/supermetry -n supermetry --reuse-values --set image.tag=0.1.23
```

Database migrations run automatically at startup. Back up first with
`kubectl -n supermetry exec deploy/supermetry -- /supermetry backup --out /data/supermetry/backup.tar`
(the backup excludes the secret key, GeoIP files and replays), then copy it out
with `kubectl cp` and delete it from the volume. In-app updates are not available on
Kubernetes; upgrade with Helm.
