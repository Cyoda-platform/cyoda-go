# Developer helper scripts

Local-development helpers. These are NOT canonical provisioning
artifacts — the canonical artifacts live under `deploy/`.

- `run-local.sh` — run cyoda-go via `go run` using the `local`
  profile (in-memory storage, mock auth).
- `run-docker-dev.sh` — run cyoda-go + Postgres via docker compose
  for local development, with a fresh JWT signing key and a
  randomized bootstrap client secret per run.
