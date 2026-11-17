# Railway

`railway.json` at the repository root is Railway's config-as-code file. It
builds `deploy/Dockerfile`, health-checks `/healthz`, runs one replica and
requires a volume at `/data`. If you deploy the published image
(`ghcr.io/velixio/supermetry`) instead, set the same options in the service
settings.

## Deploy manually

1. Create a project and add **PostgreSQL** (Database → PostgreSQL). Name the
   service `Postgres`.
2. Add a service from the Docker image `ghcr.io/velixio/supermetry:latest`,
   or from this GitHub repository (that uses `railway.json`).
3. Attach a volume mounted at `/data`.
4. Under Settings → Networking, generate a domain with target port `8787`.
5. Set the healthcheck path to `/healthz` and the replica count to 1.
6. Add the variables below and deploy.

| Variable | Value |
| --- | --- |
| `SUPERMETRY_DATABASE_URL` | `${{Postgres.DATABASE_URL}}` |
| `SUPERMETRY_BASE_URL` | `https://${{RAILWAY_PUBLIC_DOMAIN}}` |
| `SUPERMETRY_SECRET_KEY` | `${{secret(64, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/")}}` |
| `SUPERMETRY_DATA_DIR` | `/data/supermetry` |
| `SUPERMETRY_TRUSTED_PROXIES` | `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,fc00::/7,127.0.0.1,::1` |
| `SUPERMETRY_SIGNUP` | `invite` |
| `PORT` | `8787` |
| `RAILWAY_RUN_UID` | `0` |

Notes:

- The `secret()` generator returns 64 characters from the base64 alphabet.
  Those decode to 48 bytes, so the value meets the key format (base64 of at
  least 32 bytes). Outside templates, use `openssl rand -base64 32`. Never
  change the key after setup.
- Railway mounts volumes as root. `RAILWAY_RUN_UID=0` lets the container
  (normally uid 65532) write to `/data`.
- Railway's proxy reaches the service from `100.64.0.0/10`, which Supermetry
  does not trust by default. Without that range, every visitor gets the
  proxy's address and geolocation breaks. Check with `SUPERMETRY_INGEST_DIAG=1`.
- On first start the deploy logs show a one-time `.../setup?token=...` link.
  Open it to create the admin account.

## Publish the template (maintainers)

TODO: the "Deploy on Railway" buttons link to
`https://railway.com/new/template/supermetry`. That page does not exist until
the template is published.

1. Deploy a project as above, then open Project Settings → **Generate
   Template from Project** (or Workspace → Templates → New Template).
2. Check that the variables match the table above (the `${{...}}` references
   and the `secret()` generator are kept as written), and that the volume and
   domain are included.
3. Publish it, then replace the placeholder link in `README.md` and
   `deploy/README.md` with the template URL
   (`https://railway.com/deploy/<code>` or `https://railway.com/new/template/<code>`).
