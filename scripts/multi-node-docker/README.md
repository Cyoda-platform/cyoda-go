# Multi-node docker dev cluster

Developer/test tool for spinning up multiple cyoda-go containers
sharing a Postgres backend and gossip-configured for cluster mode.
Not a canonical provisioning artifact — use `deploy/` for that.

Usage:

    ./start-cluster.sh --nodes 3
    ./stop-cluster.sh
