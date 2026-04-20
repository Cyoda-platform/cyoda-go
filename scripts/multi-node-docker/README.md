# Multi-node docker dev cluster

Developer/test tool for spinning up cyoda-go containers behind an
nginx load balancer. Not a canonical provisioning artifact — use
`deploy/` for that.

## Usage

    ./start-cluster.sh --nodes 3                  # postgres (default)
    ./start-cluster.sh --nodes 1 --profile sqlite
    ./start-cluster.sh --nodes 1 --profile memory
    ./stop-cluster.sh

## Profiles

| Profile    | Backend     | Multi-node | State lives in |
|------------|-------------|------------|----------------|
| `postgres` | Postgres 17 | yes (gossip-configured cluster) | named volume `pgdata` |
| `sqlite`   | SQLite      | **no** (single-node only) | named volume `cyoda-data` |
| `memory`   | In-memory   | **no** (single-node only) | container memory (lost on restart) |

Only `postgres` shares state across nodes. With `sqlite` or `memory`,
each container has isolated storage, so `--nodes >1` is rejected.

## Per-profile overrides

Drop a `.env.<profile>` file in this directory to override any `CYODA_*`
env var for that profile. Example — `.env.postgres`:

    CYODA_POSTGRES_MAX_CONNS=25
    CYODA_LOG_LEVEL=debug

Base `.env` (auto-generated on first run) holds JWT/HMAC secrets and
bootstrap creds — profile-agnostic, delete to regenerate.
