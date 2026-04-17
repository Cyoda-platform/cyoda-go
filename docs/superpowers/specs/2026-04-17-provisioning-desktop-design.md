# Desktop provisioning — design

**Status:** Accepted
**Date:** 2026-04-17
**Scope:** Per-target packaging of cyoda for desktop users (evaluators, developers, CLI users). Consumes the shared provisioning design at `docs/superpowers/specs/2026-04-16-provisioning-shared-design.md` as input.

## Motivation

The shared provisioning work leaves desktop users with only two install paths today: `go install` and downloading an archive from GitHub Releases. Both give the bare binary with the compiled-in `memory` default. That's appropriate for Go developers, but not for evaluators who want a one-liner install that "just works" with persistent storage.

The desktop spec adds three additional install paths — Homebrew, `curl | sh`, and unsigned `.deb`/`.rpm` downloads — each dropping a config file that elevates the default to sqlite with an OS-appropriate data location. It also tightens the binary so `go install` users on macOS and Windows get correct default paths when they opt into sqlite manually.

## Audience mapping

Both of these rows are restated from the shared spec; this spec fills in the mechanics for each:

| Install path | Persona | Priority | Storage default |
|---|---|---|---|
| Desktop — packaged install (Homebrew, `.deb`, `.rpm`, `curl \| sh`) | Evaluators, CLI users | 60-second start, no dependencies, sqlite default | `sqlite` via packaged config |
| Desktop — `go install` or downloaded archive | Power users, Go developers | Bare binary, minimum magic | binary's compiled-in `memory` default |

## Prerequisite work

Two cross-cutting changes must land before the first desktop release. Both are described here but apply project-wide and are **not confined to the desktop PR**:

### Prerequisite A — binary / chart / image rename (`cyoda-go` → `cyoda`)

User-facing artifact names change to `cyoda`. The repo, Go module path, environment-variable prefix, and plugin module paths stay unchanged — only user-facing names change.

| Changed | To |
|---|---|
| `cmd/cyoda-go/` | `cmd/cyoda/` |
| `.goreleaser.yaml` build id, binary name, image name | `cyoda`; `ghcr.io/cyoda-platform/cyoda` |
| `.github/workflows/release-chart.yml` tag glob | `cyoda-*` |
| `deploy/helm/cyoda-go/` (once created by the Helm per-target plan) | `deploy/helm/cyoda/` |
| `examples/compose-with-observability/compose.yaml` image reference | `ghcr.io/cyoda-platform/cyoda:latest` |
| `scripts/dev/run-local.sh` and `scripts/dev/run-docker-dev.sh` | Invoke `./cmd/cyoda`, comments match |
| `cmd/cyoda/main.go` `printHelp()` — all usage/example lines | All `cyoda-go` → `cyoda` |
| `README.md`, `CONTRIBUTING.md` — every prose reference to the binary | All `cyoda-go` → `cyoda` |

| Unchanged |
|---|
| Go module path `github.com/cyoda-platform/cyoda-go` |
| Plugin module paths (`github.com/cyoda-platform/cyoda-go/plugins/*`) |
| Env-var prefix (`CYODA_*`) |
| GitHub repo name (`cyoda-go` — identifies the Go implementation in the cyoda-platform org) |

**Landing:** the rename lands as prerequisite commits on the existing shared-layer PR (#44) before it merges. The shared PR must not merge with stale names, since nothing has been consumed publicly yet.

### Prerequisite B — version reset across all three repos

All existing tags in the three coordinated repos are deleted and re-tagged fresh at `v0.1.0`. Safe because nothing external has been consumed (confirmed by the project lead).

| Repo | Existing tags | Action |
|---|---|---|
| `cyoda-platform/cyoda-go` | (none) | Cut `v0.1.0` at the merged-to-main commit. Also cut `plugins/memory/v0.1.0`, `plugins/postgres/v0.1.0`, and `plugins/sqlite/v0.1.0` fresh at the same commit. |
| `cyoda-platform/cyoda-go-spi` | `v0.1.0` … `v0.5.3` | `git push --delete origin <tag>` for every tag, then re-tag `v0.1.0` at current HEAD. |
| `cyoda-platform/cyoda-go-cassandra` | `v0.1.1` | `git push --delete origin v0.1.1`, then re-tag `v0.1.0` at current HEAD. |

**Order of operations:** the shared PR merges to `main`; the root repo's plugin module tags are cut; the SPI and Cassandra repos are reset; then the first app `v0.1.0` tag is pushed, which triggers `release.yml`.

## Binary-level changes

Three changes to the cyoda binary itself. They live in this spec's PR because they enable the packaged experience — not in the shared layer.

### B1 — OS-aware `defaultDBPath()` in the sqlite plugin

Current `plugins/sqlite/config.go:defaultDBPath()` is Linux-XDG-only and falls back to `~/.local/share/cyoda-go/cyoda.db` on every OS. Wrong on Windows; awkward on macOS (though acceptable per the "collapse Linux and macOS to XDG" decision in this spec).

Replace with a single branch on `runtime.GOOS`:

- **`windows`**: `%LocalAppData%\cyoda\cyoda.db` (via `os.UserCacheDir` analog for LocalAppData, or direct `%LocalAppData%` env lookup with home-dir fallback).
- **everything else (Linux + macOS)**: `$XDG_DATA_HOME/cyoda/cyoda.db` → `~/.local/share/cyoda/cyoda.db` if XDG_DATA_HOME is unset.

Also update the sqlite plugin's `ConfigVars()` metadata so `cyoda --help` surfaces the per-platform default.

Precedent for the Linux+macOS collapse: `gh` (GitHub's own Go CLI), `kubectl`, and `docker` all use XDG-style paths on macOS. Only Homebrew-managed daemon DBs (Postgres, Redis, Mongo) use the Homebrew-prefix convention, which doesn't apply to a user-level CLI tool.

### B2 — extend `LoadEnvFiles()` to autoload from standard locations

Current `app/envfiles.go:LoadEnvFiles()` reads `.env` and `.env.<profile>` from the current working directory only. Useful for dev in the repo root; useless for a packaged install where the binary is invoked from anywhere on `PATH`.

Extend the search to also autoload from well-known locations. Order of loading (later values override earlier; the real shell environment overrides everything, per existing contract):

1. System config:
   - Linux: `/etc/cyoda/cyoda.env`
   - macOS: (none — Homebrew formulas cannot cleanly write to system paths; `cyoda init` writes user config instead)
   - Windows: `%ProgramData%\cyoda\cyoda.env`
2. User config:
   - Linux + macOS: `$XDG_CONFIG_HOME/cyoda/cyoda.env` → `~/.config/cyoda/cyoda.env`
   - Windows: `%AppData%\cyoda\cyoda.env`
3. CWD: `.env`, `.env.<profile>` (existing behavior, unchanged).
4. Real shell environment (existing behavior, unchanged — always wins).

The file at each location is a standard `.env` (key=value). Missing files silently skipped, matching current behavior.

### B3 — `cyoda init` subcommand

New subcommand. Writes a starter user config.

Behavior:

1. Compute the user config path for the host OS (per B2 step 2).
2. If the file already exists and `--force` is not set: print `config already exists at <path> (use --force to overwrite)` and exit 0.
3. Otherwise, ensure the parent directory exists (`os.MkdirAll` with `0700`), then write:

   ```
   # cyoda user config — written by 'cyoda init'
   # Shell-exported vars override values here.

   CYODA_STORAGE_BACKEND=sqlite
   # CYODA_SQLITE_PATH=<computed per-OS default>   # uncomment to override
   ```

4. Print `wrote config to <path>` and exit 0.

**Does not generate JWT signing material or bootstrap credentials.** Alignment with the shared spec's "binary never generates JWT material" stance. The mock-auth startup banner continues to be the visible signal that real auth isn't configured. A future `cyoda keygen` subcommand can add key generation if demand materializes; that's explicitly out of scope here.

**Does not start any service, touch any database, or modify any other file.** It writes one file and exits.

## Packaging mechanics

### P1 — Homebrew tap

- **Tap repo:** `cyoda-platform/homebrew-cyoda-go` (must carry the `homebrew-` prefix per Homebrew's tap-naming requirement; the `-go` suffix matches the parent repo's name).
- **One-shot install for users:** `brew install cyoda-platform/cyoda-go/cyoda`.
- **Everyday install after `brew tap cyoda-platform/cyoda-go`:** `brew install cyoda`.
- **Formula generation:** GoReleaser's `brews:` stanza auto-commits an updated `cyoda.rb` formula to the tap repo on every non-prerelease `v*` release of the parent repo.
- **Formula contents:** standard — downloads the darwin or linux archive (matching the runtime arch), verifies via GoReleaser-generated SHA256, installs the `cyoda` binary to the formula's `bin/`. A `caveats` block tells the user to run `cyoda init` to enable sqlite persistence.
- **One-time setup (manual, documented in the implementation plan):**
  1. Create an empty `cyoda-platform/homebrew-cyoda-go` repo on GitHub with a brief README explaining the tap.
  2. Generate a fine-grained PAT scoped to `contents: write` on that one repo.
  3. Store the PAT as `HOMEBREW_TAP_TOKEN` in `cyoda-platform/cyoda-go`'s Actions secrets.
  4. GoReleaser's `brews:` stanza references this secret via `env:`.

### P2 — `curl | sh` installer

- **Script location in repo:** `scripts/install.sh`.
- **User invocation:**
  ```
  curl -fsSL https://raw.githubusercontent.com/cyoda-platform/cyoda-go/main/scripts/install.sh | sh
  ```
- **Script behavior:**
  1. Detect OS (`uname -s`) and arch (`uname -m`). Fail cleanly on unsupported combinations with a list of supported targets.
  2. Resolve target version: `CYODA_VERSION` env var if set, else latest non-prerelease release via `https://api.github.com/repos/cyoda-platform/cyoda-go/releases/latest`.
  3. Compute expected archive name (e.g., `cyoda_0.1.0_linux_amd64.tar.gz`).
  4. Download the archive and `SHA256SUMS` to a scratch dir.
  5. Verify SHA256. Abort on mismatch with a clear message.
  6. Extract the `cyoda` binary.
  7. Install to `$HOME/.local/bin/cyoda` (create the directory if needed). Never use `sudo`.
  8. Warn if `$HOME/.local/bin` isn't on `PATH` and print the one-line export to fix it (`.bashrc`/`.zshrc` style, per the user's default shell if detectable).
  9. Invoke `cyoda init` to write the user config.
  10. Print a concise next-steps block: how to start cyoda, link to the README's Quick Start.
- **No cosign verification in v0.1.0.** Cosign would require `cosign` on the user's machine; not worth the friction for a first release. Follow-up when we're willing to instruct users to install cosign first.
- **No `sudo` path in v0.1.0.** Users who want a system-wide install can copy the binary themselves. The installer sticks to the user's home to keep the script simple and never surprise-elevate.

### P3 — unsigned `.deb` and `.rpm` via nFPM

- **Generation:** GoReleaser's `nfpms:` stanza produces `cyoda_<version>_linux_<arch>.{deb,rpm}` attached to the GH Release alongside archives.
- **Package contents:**
  - `/usr/bin/cyoda` — the binary.
  - `/etc/cyoda/cyoda.env` — system config, nFPM `type=config` (preserves user edits across upgrades).
- **Config contents:** one line — `CYODA_STORAGE_BACKEND=sqlite`. Everything else is computed by the binary via `defaultDBPath()` and the existing env-file precedence.
- **No systemd unit, no user/group creation, no post-install scripts.** The package drops two files.
- **No apt/rpm repo hosting, no package signing.** Users download the package directly and install with `dpkg -i` or `rpm -i`. README documents this.

## Documentation

README gains an **Install** section near the top, listing the five paths with one-liner commands:

```
# macOS or Linux via Homebrew
brew install cyoda-platform/cyoda-go/cyoda

# Any Unix via curl
curl -fsSL https://raw.githubusercontent.com/cyoda-platform/cyoda-go/main/scripts/install.sh | sh

# Debian/Ubuntu
wget https://github.com/cyoda-platform/cyoda-go/releases/download/v0.1.0/cyoda_0.1.0_linux_amd64.deb
sudo dpkg -i cyoda_0.1.0_linux_amd64.deb

# Fedora/RHEL
wget https://github.com/cyoda-platform/cyoda-go/releases/download/v0.1.0/cyoda_0.1.0_linux_amd64.rpm
sudo rpm -i cyoda_0.1.0_linux_amd64.rpm

# From source (Go toolchain)
go install github.com/cyoda-platform/cyoda-go/cmd/cyoda@latest
```

Plus a **Configuration** subsection explaining the env-file autoload hierarchy (shell env > user config > system config > compiled defaults) and pointing to `.env.sqlite.example`.

## Target artifacts produced by the desktop PR

| Artifact | Purpose |
|---|---|
| `scripts/install.sh` | `curl \| sh` installer |
| Updates to `.goreleaser.yaml` | `brews:` stanza + `nfpms:` stanza |
| Updates to `cmd/cyoda/main.go` (post-rename) | `init` subcommand |
| Updates to `app/envfiles.go` | Autoload from user + system paths |
| Updates to `plugins/sqlite/config.go` | OS-aware `defaultDBPath()` |
| Tests for all of the above | TDD |
| Updates to `README.md` and `CONTRIBUTING.md` | Install section, Configuration subsection |
| A short documentation addition for the Homebrew tap one-time setup | Describes creating the tap repo + PAT, for the maintainer |

## Downstream implementation plan

The implementation plan generated from this spec covers the desktop-layer work:

1. **Prerequisite A (rename):** land rename commits on the shared PR #44. The desktop PR starts from a branch off the post-rename main, once #44 merges.
2. **Prerequisite B (version reset):** manually coordinated; not code. Script in the plan for the delete-and-retag steps, run by a maintainer after the shared PR merges and before the desktop PR's first triggered release.
3. Extend `app/envfiles.go` autoload search to user + system config locations (TDD).
4. Fix `plugins/sqlite/config.go:defaultDBPath()` to be OS-aware, with updated `ConfigVars()` metadata (TDD).
5. Add `cyoda init` subcommand (TDD).
6. Add `nfpms:` stanza to `.goreleaser.yaml`.
7. Add `brews:` stanza to `.goreleaser.yaml`.
8. Write `scripts/install.sh`, including a CI shellcheck + smoke test.
9. Create the one-time GitHub setup: empty `cyoda-platform/homebrew-cyoda-go` repo, `HOMEBREW_TAP_TOKEN` secret (documented, manual).
10. Update `README.md` (Install + Configuration sections), `CONTRIBUTING.md`, and `.env.sqlite.example` pointers.

## Out of scope (deferred)

- **Windows packaging beyond `.zip`.** No Scoop/Chocolatey/Winget/MSI in v0.1.0. The binary builds for Windows today; users who want it unzip it themselves.
- **Signed `.deb`/`.rpm` + hosted apt/rpm repo.** Would unlock `apt install cyoda` / `dnf install cyoda` UX; requires GPG signing keys, CI secrets, and repo hosting (Cloudsmith / packagecloud / self-hosted with aptly). Several days of work plus a signing-key lifecycle.
- **`brew services` integration.** Launchd plist shipped via the formula so `brew services start cyoda` works. Adds a service-mode UX; orthogonal to the CLI-tool UX this spec delivers.
- **systemd unit in `.deb`/`.rpm`.** Same service-mode argument. A future "cyoda-server" meta-package could bundle the systemd unit.
- **macOS `.pkg` installer.** Alternative to Homebrew; only needed if we want signed installers outside Homebrew's distribution.
- **`cyoda keygen` or JWT generation in `cyoda init`.** Deliberate alignment with the shared spec's "binary never generates JWT material" stance.
- **cosign verification in `install.sh`.** Follow-up once the user-facing friction is acceptable.
- **`install.cyoda.io` redirect.** Raw-GitHub URL is fine for v0.1.0.
- **Desktop "service" installation on any platform.** Users run `cyoda` from their terminal; machine-level service integration is handled by Helm for operators, not desktop for users.
