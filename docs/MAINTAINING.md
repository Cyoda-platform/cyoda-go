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

## One-time setup: version reset across coordinated repos

Before the first public release cuts, existing pre-public tags in the
three coordinated repos are deleted and recreated at `v0.1.0`. See
`docs/superpowers/specs/2026-04-17-provisioning-desktop-design.md`
(Prerequisite B) for the exact `git push --delete` and re-tagging
commands. Safe because nothing has been consumed publicly yet;
after the first release, tags are immutable by convention.
