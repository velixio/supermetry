# Update release signing

Supermetry's in-app updater accepts only canonical release manifests signed by a
public key embedded in the installed binary. The GitHub release workflow builds
the binary archives and multi-platform image first, records their exact sizes,
SHA-256 hashes, image-index digest and platform digests, and creates the GitHub
Release only after signing that metadata.

The private key belongs only in the protected GitHub `release` environment. Do
not print it, commit it, add it to a dotenv file, or store it in a repository or
workflow artifact.

## One-time setup

Generate the key on a trusted workstation in a private temporary directory. The
command refuses a directory accessible by group or other users and refuses to
overwrite either key file.

```sh
key_dir="$(mktemp -d)"
chmod 700 "$key_dir"
go run ./cmd/release-manifest --generate-key-dir "$key_dir"

gh api --method PUT repos/velixio/supermetry/environments/release >/dev/null
gh secret set RELEASE_SIGNING_KEY --env release < "$key_dir/RELEASE_SIGNING_KEY"
gh variable set RELEASE_SIGNING_PUBLIC_KEY < "$key_dir/RELEASE_SIGNING_PUBLIC_KEY"
gh variable set RELEASE_TRUSTED_PUBLIC_KEYS < "$key_dir/RELEASE_SIGNING_PUBLIC_KEY"
gh variable set RELEASE_MINIMUM_VERSION --body v0.1.0

rm -f "$key_dir/RELEASE_SIGNING_KEY" "$key_dir/RELEASE_SIGNING_PUBLIC_KEY"
rmdir "$key_dir"
```

In the repository settings, configure the `release` environment with required
reviewers and prevent unreviewed branches or tags from using it. The public keys
are repository variables because the archive and image build jobs must embed
them; the private key remains an environment secret available only to the final
publish job. `RELEASE_SIGNING_PUBLIC_KEY` is the active key matching the private
secret. `RELEASE_TRUSTED_PUBLIC_KEYS` is the comma-separated keyring embedded in
binaries and defaults to the active key when unset. `RELEASE_MINIMUM_VERSION`
is optional and defaults to `v0.1.0`.

Keep workflow logs free of shell tracing (`set -x`). GitHub cannot show the
stored secret again; if organizational policy requires a recovery copy, put it
in an approved encrypted secret manager before deleting the temporary file.

## Release output and verification

For a tag such as `v1.2.3`, the workflow publishes the normal archives,
installers and `SHA256SUMS`, plus:

- `release-manifest.json`, canonical JSON with source, schema, archive and image
  metadata;
- `release-manifest.json.sig`, the detached Ed25519 signature;
- the `supermetry-signed-release-metadata` workflow artifact containing those
  two files.

The signed binary set contains eight targets: Linux amd64, arm64, armv6 and
armv7; macOS amd64 and arm64; and Windows amd64 and arm64. The armv6 and armv7
names intentionally distinguish the two 32-bit ARM instruction targets even
though both are built with Go's `GOARCH=arm`. Container images remain Linux
amd64 and arm64 only.

The publish job fails closed if either key is absent or malformed, the keys do
not match, the image index lacks either supported Linux platform, an artifact
does not match the expected naming convention, or manifest validation fails.
It does not create a partial GitHub Release before those checks pass.

These checks inspect a published release without reading the private key:

```sh
tag=v1.2.3
check_dir="$(mktemp -d)"
gh release download "$tag" --repo velixio/supermetry --dir "$check_dir"
(cd "$check_dir" && sha256sum --check --ignore-missing SHA256SUMS)
jq . "$check_dir/release-manifest.json"
docker buildx imagetools inspect "ghcr.io/velixio/supermetry@$(jq -r .container.digest "$check_dir/release-manifest.json")"
rm -f "$check_dir"/*
rmdir "$check_dir"
```

Installed updater helpers additionally verify the detached signature against
their embedded key before trusting any hash, size, schema field or image digest.

## Rotation

Never replace the trusted key and signing key in one release: existing installs
would reject that release. First set `RELEASE_TRUSTED_PUBLIC_KEYS` to
`old_public,new_public`, while leaving the active public key and private secret
unchanged, then ship a bridge release signed by the old key. After that release
is widely available, switch `RELEASE_SIGNING_PUBLIC_KEY` and
`RELEASE_SIGNING_KEY` to the new matching pair while leaving both keys trusted.
Keep the old public key in the helper until the declared
minimum update version guarantees every eligible source release trusts the new
key. A compromised or lost private key requires this same bridge process when
the old key remains usable; otherwise affected installations must use the
documented manual upgrade path.
