# Maintaining cyoda-go

Notes for cyoda-go maintainers on tasks that aren't part of the regular
development workflow.

## One-time setup: Homebrew tap release automation

Before the first `v*` tag triggers the GoReleaser Homebrew-publishing
job, these steps must be completed once.

### 1. Create the empty tap repository

- New repo: `cyoda-platform/homebrew-cyoda-go` (public, empty).
- `README.md` in the tap repo: a short paragraph explaining the tap
  and linking back to this main repo. GoReleaser will push `cyoda.rb`
  on every release.

### 2. Create the GitHub App

A GitHub App (not a personal access token) mints short-lived
installation tokens for the release workflow. Advantages over a PAT:
org-owned, no human account attached, no expiration to track, audit
trail is clean.

1. Navigate to `https://github.com/organizations/cyoda-platform/settings/apps`.
2. Click **New GitHub App**.
3. Fill in:
   - App name: `cyoda-platform-release-bot` (must be globally unique
     across all GitHub Apps; add a suffix if taken).
   - Homepage URL: `https://github.com/cyoda-platform/cyoda-go`
   - Webhook: uncheck **Active** (no webhook needed).
   - Permissions → **Repository permissions**:
     - **Contents**: Read and write
   - Permissions → **Account permissions**: (leave all unset)
   - Where can this GitHub App be installed?: **Only on this account**.
4. Click **Create GitHub App**.
5. After creation, note the numeric **App ID** at the top of the App
   settings page (typically 6–7 digits).
6. Scroll to **Private keys** and click **Generate a private key**. A
   `.pem` file downloads to your browser — keep it for the next step.

### 3. Install the App on the tap repo

1. On the App settings page, click **Install App** in the left sidebar.
2. Choose the `cyoda-platform` org.
3. Under **Repository access**, select **Only select repositories** and
   add `cyoda-platform/homebrew-cyoda-go`. Do NOT install on the whole
   org — the App's scope must be minimal.
4. Click **Install**.

### 4. Configure secrets in the cyoda-go repo

1. Navigate to `https://github.com/cyoda-platform/cyoda-go/settings/secrets/actions`.
2. Add secret `HOMEBREW_TAP_APP_ID`: the numeric App ID from step 2.5.
3. Add secret `HOMEBREW_TAP_APP_KEY`: the full contents of the `.pem`
   file from step 2.6, including the `-----BEGIN PRIVATE KEY-----`
   and `-----END PRIVATE KEY-----` lines.
4. Delete the local `.pem` file from your machine. The private key
   only needs to live in the Actions secret now.

### 5. Verify

On the next non-prerelease `v*` tag push, the release workflow's
**Generate Homebrew tap token** step mints a short-lived installation
token, GoReleaser uses it to push `cyoda.rb` to `homebrew-cyoda-go`,
and the tap repo's commit history shows `cyoda-platform-release-bot`
as the commit author.

If the step fails with a 401: check that the App is installed on the
tap repo (step 3), and that `HOMEBREW_TAP_APP_ID` / `HOMEBREW_TAP_APP_KEY`
are both set.

## Key rotation

If the private key is compromised or simply needs rotation:

1. App settings → **Private keys** → **Generate a private key** for a
   new key.
2. Immediately update `HOMEBREW_TAP_APP_KEY` in the cyoda-go repo
   secrets with the new `.pem` contents.
3. App settings → delete the old private key.
4. Delete the local `.pem` from your machine.

No release-workflow code changes are needed — the App ID is stable
across rotations.

## First-release checklist

Before pushing the first `v0.1.0` tag, execute these in order. Every
step is validated by the release workflow's pre-flight; skipping one
causes a clean abort, but the overall sequence is faster to get right
once than to diagnose three failed releases.

### 1. Version reset across coordinated repos

Existing pre-public tags in `cyoda-go-spi` and `cyoda-go-cassandra`
are deleted and recreated at `v0.1.0`:

```bash
# In cyoda-go-spi repo:
for tag in $(git tag --list 'v*'); do git push --delete origin "$tag"; done
git tag v0.1.0
git push origin v0.1.0

# In cyoda-go-cassandra repo:
git push --delete origin v0.1.1
git tag v0.1.0
git push origin v0.1.0
```

Safe because nothing has been consumed publicly; after first release,
tags are immutable by convention (see the project's
`feedback_go_module_tags_immutable.md`).

### 2. Cut plugin module tags

Plugin modules are tagged at a commit on `cyoda-go`'s `main` branch.
Must happen **before** step 3, otherwise `go mod tidy` in step 3 can't
resolve the pinned versions.

```bash
# In cyoda-go (main branch, at the commit to be released):
git tag plugins/memory/v0.1.0
git tag plugins/postgres/v0.1.0
git tag plugins/sqlite/v0.1.0
git push origin plugins/memory/v0.1.0 plugins/postgres/v0.1.0 plugins/sqlite/v0.1.0
```

### 3. Drop the `replace` directives and pin plugin module versions

Root `go.mod` currently has three `replace` directives pointing at
`./plugins/*` for dev-time convenience. Release builds must resolve
to published modules, not local paths — so these must be dropped.
The release workflow's pre-flight rejects any `replace` and would
abort cleanly, but removing them explicitly is cleaner:

```bash
go mod edit -dropreplace github.com/cyoda-platform/cyoda-go/plugins/memory
go mod edit -dropreplace github.com/cyoda-platform/cyoda-go/plugins/postgres
go mod edit -dropreplace github.com/cyoda-platform/cyoda-go/plugins/sqlite
go mod tidy
```

`go mod tidy` pins the plugin modules to the tags you just cut in
step 2. Review the diff to `go.mod` and `go.sum` — you should see
the `require` block gain entries for each plugin at `v0.1.0`, and
the `replace` directives should be gone.

```bash
git add go.mod go.sum
git commit -m "chore: drop replace directives; pin plugin modules at v0.1.0"
git push origin main
```

**Why not delete the replace directives now?** Because pre-public
development uses `GOWORK=off go build ./...` occasionally (reviews,
snapshot checks) and the replaces make that work without requiring
every plugin change to be tagged and pushed. After the first release
they become vestigial and can stay dropped; dev-time workflows use
`go.work` for local composition going forward.

### 4. Homebrew tap setup (from the section above)

Create `cyoda-platform/homebrew-cyoda-go` repo, create the GitHub App,
install on the tap repo, store the App ID and private key as Actions
secrets. See "One-time setup: Homebrew tap release automation" above.

### 5. Verify CI is green

Push a commit to `main` (or open a small PR) and confirm both the
`test` and `per-module-hygiene` CI jobs pass. Don't push the release
tag if CI is red.

### 6. Cut the release

```bash
git tag v0.1.0
git push origin v0.1.0
```

`release.yml` fires: pre-flight module verification, build binaries,
multi-arch image to GHCR, keyless cosign signing, SBOM attachment,
Homebrew formula commit to the tap, GitHub Release with all archives.

The workflow runs ~5 minutes. Watch it in the Actions tab and
verify:

- Release appears on the Releases page with all expected archives,
  `.deb`/`.rpm` packages, `SHA256SUMS`, cosign signatures, SBOMs.
- `ghcr.io/cyoda-platform/cyoda:v0.1.0` and `:latest` manifests exist.
- `cyoda-platform/homebrew-cyoda-go` shows a new commit updating
  `Formula/cyoda.rb` to `v0.1.0`.

### 7. (Optional) Smoke-test each install path

```bash
# Homebrew (macOS or Linux):
brew install cyoda-platform/cyoda-go/cyoda
cyoda --help

# curl | sh (any Unix):
curl -fsSL https://raw.githubusercontent.com/cyoda-platform/cyoda-go/main/scripts/install.sh | sh

# Debian:
wget https://github.com/cyoda-platform/cyoda-go/releases/latest/download/cyoda_linux_amd64.deb
sudo dpkg -i cyoda_linux_amd64.deb
```

## Pre-release testing

Before cutting `v0.1.0`, you can exercise the release pipeline via a
prerelease tag:

```bash
git tag v0.1.0-rc.1
git push origin v0.1.0-rc.1
```

This fires the full release workflow, producing a prerelease GitHub
Release, images tagged `:v0.1.0-rc.1` (but NOT `:latest`), cosign
signatures, and SBOMs. The Homebrew tap, chart appVersion bump, and
`install.sh` / `.deb` / `.rpm` user-facing paths are all unaffected
because:

- `brews:` has `skip_upload: auto` — prereleases don't commit to the tap.
- `:latest` manifest has `skip_push: '{{ .Prerelease }}'` — doesn't move.
- `bump-chart-appversion.yml` filters out tags containing `-`.
- `install.sh` uses the GitHub `/releases/latest` API which hides prereleases.

Delete the rc release afterwards if desired:

```bash
gh release delete v0.1.0-rc.1 --cleanup-tag --yes
```
