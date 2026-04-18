# Maintaining cyoda-go

## Prerequisites for chart releases

Before the first `cyoda-*` tag is pushed (chart release), the repo
maintainer must enable GitHub Pages:

1. Repo Settings → Pages
2. Source: "Deploy from a branch"
3. Branch: `gh-pages` / `(root)`
4. Save

The `gh-pages` branch is created by `chart-releaser-action` on first
release and does not need to pre-exist.

The `release-chart.yml` workflow verifies Pages is configured via
`gh api repos/:owner/:repo/pages` and fails fast with an actionable
message if not — but the setup must happen once, by a human, before
any `cyoda-*` tag.

## Chart version vs binary appVersion

Two independent tag streams:

- `v*` (e.g. `v0.2.0`): binary release. Triggers:
  - `release.yml` (binaries + container image)
  - `bump-chart-appversion.yml` (opens PR bumping chart appVersion)
- `cyoda-*` (e.g. `cyoda-0.2.0`): chart release. Triggers:
  - `release-chart.yml` (packages + publishes to gh-pages)

Standard pattern: merge the appVersion-bump PR, optionally also bump
chart `version:` in the same PR if shipping a chart release, then tag
`cyoda-<new-version>` after merge.
