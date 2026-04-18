# cyoda Helm chart

Production-ready Helm deployment of cyoda-go backed by an external
Postgres, fronted by Gateway API (default) or a still-maintained
Ingress controller (transitional).

Chart version: 0.1.0 — AppVersion: pinned by `bump-chart-appversion.yml`
on each binary release.

## Installation

### Prerequisites

- Helm v4.1+ recommended (chart is `apiVersion: v2` and also installs
  cleanly from Helm v3.16+).
- Kubernetes 1.31+ (Gateway API CRDs required if using `gateway.enabled=true`).
- An existing Postgres instance reachable from the cluster, with a
  dedicated database and role for cyoda.
- A JWT RSA signing key. Generate with:
  ```bash
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 \
    -out jwt-signing-key.pem
  ```

### Create the required Secrets

```bash
kubectl create namespace cyoda

kubectl -n cyoda create secret generic cyoda-dsn \
  --from-literal=dsn='postgres://cyoda:REDACTED@pg.example.com:5432/cyoda?sslmode=require'

kubectl -n cyoda create secret generic cyoda-jwt \
  --from-file=signing-key.pem=./jwt-signing-key.pem
```

### Install

```bash
helm repo add cyoda https://cyoda-platform.github.io/cyoda-go
helm repo update

helm install cyoda cyoda/cyoda -n cyoda \
  --set postgres.existingSecret=cyoda-dsn \
  --set jwt.existingSecret=cyoda-jwt \
  --set gateway.parentRefs[0].name=platform-gateway \
  --set gateway.parentRefs[0].namespace=gateway-system \
  --set gateway.http.hostnames[0]=cyoda.example.com \
  --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com
```

### Enabling the bootstrap M2M client (optional)

By default the chart does NOT provision a bootstrap M2M client
(`bootstrap.clientId=""`). The binary runs cleanly in jwt mode via
JWKS / external signing keys alone.

To enable bootstrap, set `bootstrap.clientId`:

```bash
helm upgrade cyoda cyoda/cyoda -n cyoda --reuse-values \
  --set bootstrap.clientId=cyoda-bootstrap
```

The chart auto-generates the secret (or use
`bootstrap.clientSecret.existingSecret` for GitOps).

### Scale to 3 replicas (cluster mode)

```bash
helm upgrade cyoda cyoda/cyoda -n cyoda \
  --reuse-values \
  --set replicas=3
```

No mode flip needed — cluster mode is always on; at replicas=1 it runs
as a "cluster of one".

## Using with GitOps (Argo CD)

The chart auto-generates the HMAC Secret via Helm's `lookup` function
on first install. **This does not work with Argo CD's default render
path** (which uses `helm template`, where `lookup` is a no-op). Without
mitigation, Argo CD would re-randomize the HMAC secret on every
reconcile, breaking gossip encryption and inter-node HTTP dispatch auth.

The chart catches this at render time and fails with an actionable
error message. To fix:

**Option A: pre-create the Secrets and pass `existingSecret`:**

```bash
kubectl -n cyoda create secret generic cyoda-hmac \
  --from-literal=secret=$(openssl rand -hex 32)
```

```yaml
cluster:
  hmacSecret:
    existingSecret: cyoda-hmac
```

If bootstrap is also enabled (`bootstrap.clientId` is set), the
`bootstrap.clientSecret.existingSecret` escape hatch is only relevant
when the bootstrap M2M client is active — omit it when
`bootstrap.clientId=""`.

```bash
kubectl -n cyoda create secret generic cyoda-bootstrap \
  --from-literal=secret=$(openssl rand -hex 32)
```

```yaml
bootstrap:
  clientId: cyoda-bootstrap
  clientSecret:
    existingSecret: cyoda-bootstrap
```

**Option B: use external-secrets-operator** to sync from a real secret
store (Vault, AWS Secrets Manager, etc.) into the Secret names.

## Reference topology (Gateway API + Cloudflare tunnel)

```
     ┌─────────────────────────┐
     │ External origin         │
     │ (Cloudflare tunnel etc) │
     └──────────┬──────────────┘
                │
     ┌──────────▼──────────────┐
     │ Gateway (platform ns)   │
     │ envoy-gateway, contour, │
     │ cilium, istio…          │
     └──┬────────────────────┬─┘
        │ HTTPRoute          │ GRPCRoute
        │                    │
    ┌───▼───┐            ┌───▼───┐
    │Service│            │Service│
    │cyoda: │            │cyoda: │
    │ http  │            │ grpc  │
    └───┬───┘            └───┬───┘
        │                    │
        └─────────┬──────────┘
                  │
             ┌────▼────┐
             │  cyoda  │
             │ pod(s)  │
             └─────────┘
```

## Migrating from ingress-nginx

`ingress-nginx` was retired by SIG Network in March 2026. Use the
`ingress` values block in this chart as a transitional affordance
until you've migrated to Gateway API. For migration tooling, see
[Ingress2Gateway 1.0](https://kubernetes.io/blog/2026/03/20/ingress2gateway-1-0-release/).

## Values reference

See [`values.yaml`](./values.yaml). Every key is documented inline.

## Troubleshooting

### "cluster.hmacSecret.existingSecret is required when the chart is rendered without live cluster access"

You're running `helm template`, `helm install --dry-run`, Argo CD
default path, or installing into a not-yet-created namespace. See
"Using with GitOps" above.

### `helm upgrade` hangs on the migration Job

Check logs: `kubectl logs -n cyoda job/cyoda-migrate-<release-revision>`.
If the migration is slow, increase `migrate.activeDeadlineSeconds`.
If the Job fails permanently, Helm rolls back values and old pods keep
serving — investigate before retrying.

### `CYODA_*_FILE` in `extraEnv` causes install to fail with duplicate env

Remove it. The chart sets all four credential env vars; to change a
credential, change the referenced `existingSecret`.
