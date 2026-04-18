# Provisioning: Helm Chart — Design

**Status:** draft · **Date:** 2026-04-17 · **Target:** Kubernetes deployment via Helm

**Depends on:** [2026-04-16-provisioning-shared-design.md](./2026-04-16-provisioning-shared-design.md) (shared foundations — binary behavior, schema-compatibility contract, secret hygiene, production floors)

**Sibling specs:** desktop (shipped PR #45), docker (shipped PR #54)

---

## 1. Scope and goals

This deliverable produces the Helm chart operators use to run cyoda-go on
Kubernetes, plus the small binary-side changes the chart requires, plus the CI
that validates the chart on every PR.

### In scope

1. **Helm chart** at `deploy/helm/cyoda/` — production-ready v0.1.0 chart that a
   cluster operator can install with `helm install cyoda cyoda/cyoda -f values.yaml`
   and get a working single-replica-or-multi-replica cyoda-go backed by an
   external Postgres, fronted by Gateway API (default) or a still-maintained
   Ingress controller (transitional).
2. **Binary-side changes** folded in under Gate 6 (resolve, don't defer):
   - New `cyoda migrate` subcommand that runs schema migrations and exits.
   - `_FILE` suffix support for every credential-shaped env var
     (`CYODA_POSTGRES_URL`, `CYODA_JWT_SIGNING_KEY`, `CYODA_HMAC_SECRET`,
     `CYODA_BOOTSTRAP_CLIENT_SECRET`). Reads value from the file named by
     `<VAR>_FILE` when the plain `<VAR>` is unset; `_FILE` wins if both are set.
   - Removal of the stdout-print-on-generate behavior for
     `CYODA_BOOTSTRAP_CLIENT_SECRET`. In `jwt` mode, the env var is now
     required; startup fails fast if unset. In `mock` mode, ignored.
3. **Chart CI** at `.github/workflows/helm-chart-ci.yml` with two layers:
   - Layer 1 (every chart-affecting PR): `helm lint`, `helm template` with
     multiple values overlays, `kubeconform` against Kubernetes 1.31 + Gateway
     API 1.2 schemas.
   - Layer 2 (chart-affecting PRs + `main`): `ct install` on a kind cluster
     with a Postgres sidecar and Envoy Gateway, smoke test on `/readyz`.
4. **Activation of the two pre-stub release workflows** that already live in
   the repo:
   - `release-chart.yml` — triggered by `cyoda-*` tags, uses
     `helm/chart-releaser-action@v1` to publish the chart to the `gh-pages`
     branch as a Helm repository (served at
     `https://cyoda-platform.github.io/cyoda-go`).
   - `bump-chart-appversion.yml` — triggered by binary `v*` release tags
     (non-prerelease), opens a PR bumping `Chart.yaml` `appVersion` to match.

### Out of scope (filed as follow-up issues — see §8)

- Layer 3 CI: multi-replica cluster-mode install with gossip coordination.
- `helm upgrade` migration-path testing (requires two chart versions).
- Ingress2Gateway-based migration guide for operators.
- Gateway API `PolicyAttachment` patterns (rate limiting, auth filters).
- Chart v0.2+ optional features (HPA, NetworkPolicy, PodMonitor alternative,
  external-secrets-operator integration).
- NetworkPolicy example restricting admin-port ingress to Prometheus namespace.

### Non-goals

- Bundled Postgres as a chart subchart. This was considered (an earlier brainstorm
  iteration landed on "Bitnami postgresql subchart, disabled by default") and
  then rejected: the chart's operator audience operates Kubernetes clusters and
  has a Postgres story of their own; the Bitnami ecosystem's 2025-2026
  licensing turbulence adds dependency-tracking cost; and the "I just want to
  try it" path is already served by `docker compose up` (shipped in PR #54).
  **The chart has zero `Chart.yaml` dependencies.**
- sqlite and memory storage backends in Helm. The ConfigMap hardcodes
  `CYODA_STORAGE_BACKEND=postgres`; no user-facing knob to override.
  sqlite-in-a-pod needs a PVC, breaks cluster mode, and defeats the chart's
  value proposition; those workloads are served by the desktop binary or
  Docker compose.
- Chart-managed TLS certificates. Operators use cert-manager (or equivalent)
  and reference generated certs via `tls.secretName` in the routing config.
- Chart-rendered `Gateway` resource. Operators are expected to run a shared
  platform Gateway; the chart renders only `HTTPRoute` and `GRPCRoute` that
  `parentRefs` into it. Rendering a per-app Gateway is an anti-pattern that
  creates N LoadBalancers for N apps.

---

## 2. Directory layout

```
deploy/helm/cyoda/
├── Chart.yaml                  # name=cyoda, version=0.1.0, appVersion pinned to a concrete binary tag
├── values.yaml                 # documented values; sane defaults
├── values.schema.json          # JSON Schema — helm 3 native validation
├── README.md                   # operator-facing; Gateway API reference topology; secret setup; upgrade/rollback notes
├── .helmignore
└── templates/
    ├── _helpers.tpl            # cyoda.fullname, cyoda.labels, cyoda.selectorLabels, cyoda.serviceAccountName, cyoda.hmacSecretName, cyoda.bootstrapSecretName
    ├── NOTES.txt               # post-install: configured hostnames, how to retrieve generated secrets, next steps
    ├── serviceaccount.yaml     # dedicated SA, automountServiceAccountToken=false
    ├── statefulset.yaml        # the cyoda workload — always StatefulSet (see §3)
    ├── service.yaml            # ClusterIP "cyoda" with named ports: http, grpc, metrics
    ├── service-headless.yaml   # headless "cyoda-headless" for gossip peer discovery
    ├── configmap.yaml          # non-secret env (ports, IAM mode, backend=postgres, etc.)
    ├── secret-hmac.yaml        # rendered only when NOT using existingSecret; lookup-based auto-gen
    ├── secret-bootstrap.yaml   # same pattern, for CYODA_BOOTSTRAP_CLIENT_SECRET
    ├── pdb.yaml                # rendered when replicas > 1
    ├── job-migrate.yaml        # pre-install + pre-upgrade hook; runs `cyoda migrate`
    ├── servicemonitor.yaml     # rendered when monitoring.serviceMonitor.enabled=true
    ├── gateway-httproute.yaml  # rendered when gateway.enabled=true
    ├── gateway-grpcroute.yaml  # same
    ├── ingress-http.yaml       # rendered when ingress.enabled=true (transitional path)
    ├── ingress-grpc.yaml       # same; annotations seeded with nginx-ingress backend-protocol=GRPC
    └── tests/
        └── test-readyz.yaml    # `helm test cyoda` hook: curl /readyz via port-forward-style pod
```

### Principles enforced by the layout

- **One resource kind per template file**, named for the kind. Matches
  Bitnami/Grafana/Loki convention. Readers find things by filename.
- **`values.schema.json` validates invariants at render time.** helm 3+
  validates values against the schema before template rendering, so bad inputs
  fail with a clear error rather than rendering silently-broken manifests.
  The schema enforces:
  - `gateway.enabled && ingress.enabled` → rejected (they conflict on routing).
  - `gateway.enabled && len(gateway.parentRefs) == 0` → rejected.
  - `ingress.enabled && (ingress.http.host == "" || ingress.grpc.host == "")` → rejected.
  - `replicas >= 1`.
  - `postgres.existingSecret` and `jwt.existingSecret` are required non-empty strings.

There is no `storage.backend` values knob. The ConfigMap hardcodes
`CYODA_STORAGE_BACKEND=postgres` unconditionally. Operators who want
sqlite/memory are not the Helm chart's audience (they have the desktop
binary or Docker compose). Adding a values knob for it is a footgun
(sqlite-in-a-pod needs a PVC; memory backend is process-local).
- **`_helpers.tpl` holds all shared template logic.** Individual templates stay
  focused on their one resource.

---

## 3. Workload and networking

### StatefulSet (always)

The chart renders the cyoda workload as a StatefulSet regardless of replica
count. Reasons:

- **Stable network identity.** `metadata.name` of each pod is predictable
  (`cyoda-0`, `cyoda-1`, ...), which lets the chart derive
  `CYODA_NODE_ID` from the pod name via the Downward API — no operator
  config needed, works at any replica count.
- **No workload-kind flip on scale-up.** `helm upgrade` cannot cleanly
  transition a Deployment → StatefulSet (the kind field is immutable on the
  resource path). Locking StatefulSet always means scaling from 1 → N is a
  values change, not a chart-breaking migration.
- **No operational penalty at `replicas: 1`.** StatefulSet-with-one-replica
  is a well-established pattern (Grafana, Gitea, single-node Postgres
  operators). Empty `volumeClaimTemplates` means it behaves identically to a
  Deployment from the operator's point of view.

### Cluster mode always on

There is no `cluster.enabled` flag. The chart wires cluster mode
unconditionally because:

- Binary already supports a single-member cluster path (see
  `internal/cluster/registry/gossip.go` — logs "no seeds configured,
  proceeding as cluster of one"). Cluster-of-one is a first-class runtime
  mode.
- The only friction `cluster.enabled=false` ever saved was "operator doesn't
  need to provide an HMAC secret." The chart auto-generates the HMAC secret
  via `lookup` (see §4), so that friction is gone.
- **Scaling is a one-flag change:** `helm upgrade --set replicas=3`. No mode
  flip, no second flag, no values coordination. The StatefulSet rolls; new
  pods discover each other via the headless service; `gossip.go` transitions
  from "cluster of 1" to "cluster of 3".

### Pod specification

Key elements (condensed — full YAML lives in the implementation plan):

- **Security context** (pod + container):
  - `runAsNonRoot: true`, `runAsUser: 65532`, `fsGroup: 65532` (distroless
    `nonroot` UID).
  - `readOnlyRootFilesystem: true`. cyoda writes nothing to local disk when
    backed by Postgres.
  - `allowPrivilegeEscalation: false`, `capabilities: { drop: [ALL] }`,
    `seccompProfile: { type: RuntimeDefault }`. Pod Security "restricted"
    profile compliant.
- **ServiceAccount:** dedicated `cyoda` SA per install, no RBAC bindings
  (cyoda doesn't talk to the kube API). `automountServiceAccountToken: false`
  on the pod — defense in depth against a stolen pod-local token.
- **Ports on the container:** `http:8080`, `grpc:9090`, `metrics:9091`,
  `gossip:7946`.
- **Downward API env:** `CYODA_NODE_ID` from `metadata.name`,
  `POD_NAMESPACE` from `metadata.namespace`, used to compute
  `CYODA_NODE_ADDR` = `http://$(CYODA_NODE_ID).cyoda-headless.$(POD_NAMESPACE).svc.cluster.local:9090`.
- **Config env:** `CYODA_STORAGE_BACKEND=postgres`, `CYODA_IAM_MODE=jwt`,
  `CYODA_REQUIRE_JWT=true`, `CYODA_POSTGRES_AUTO_MIGRATE=false`
  (migrations run in the Job, not the pod), `CYODA_ADMIN_BIND_ADDRESS=0.0.0.0`
  (necessary for ServiceMonitor scraping from the `monitoring` namespace).
- **Credential env (all `_FILE`-form):** `CYODA_POSTGRES_URL_FILE`,
  `CYODA_JWT_SIGNING_KEY_FILE`, `CYODA_HMAC_SECRET_FILE`,
  `CYODA_BOOTSTRAP_CLIENT_SECRET_FILE`. All four point at
  `/etc/cyoda/secrets/<name>`.
- **`extraEnv`:** open-ended operator env injection. Typical use: OTel
  (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, etc.), feature flags.
  The chart does not render an OTel-specific schema; operators wire OTel
  through `extraEnv`.
- **Probes:** `livenessProbe` on `/livez`, `readinessProbe` on `/readyz`,
  both on the `metrics` port (admin listener). `initialDelaySeconds: 10/5`,
  `periodSeconds: 10/5`, conservative defaults.
- **Volumes:**
  - `secrets` (`projected`, `defaultMode: 0400`): mounts all four
    credentials at `/etc/cyoda/secrets/` in a single volume. Each source
    references the appropriate Secret (operator-provided for DSN/JWT,
    chart-managed for HMAC/bootstrap) and projects into the expected
    filename.
  - `tmp` (`emptyDir: {}`): mounted at `/tmp`. Safety net for any Go stdlib
    call that uses `os.TempDir()`.
- **Resources:** defaults `requests: {cpu: 100m, memory: 256Mi}`,
  `limits: {cpu: 1000m, memory: 512Mi}`. Operators override via
  `resources` in values.
- **`volumeClaimTemplates: []`** — intentionally empty.

### Services

- **`cyoda`** (ClusterIP): single Service, three named ports.
  `http:8080 → containerPort http`, `grpc:9090 → containerPort grpc`,
  `metrics:9091 → containerPort metrics`. Selector matches the StatefulSet
  pod labels. This is the industry convention (Grafana, Prometheus, Loki,
  Tempo, Elasticsearch, Keycloak, MinIO all do this); separating the admin
  port into its own Service is reserved for specialized namespace-scoped
  NetworkPolicy patterns and was considered and rejected.
- **`cyoda-headless`** (ClusterIP, `clusterIP: None`): single port
  `gossip:7946`. `publishNotReadyAddresses: true` so peers discover each
  other before readiness passes — necessary to bootstrap a cluster
  (otherwise a cluster-of-3 never reaches ready because pods can't find
  peers until they're ready, and they can't be ready until they find peers).

### Routing — Gateway API by default, Ingress transitional

**Context.** The Kubernetes SIG-Network `ingress-nginx` project entered
retirement in March 2026 — no further releases, bugfixes, or security
patches. The community successor is the Gateway API
(`gateway.networking.k8s.io`), GA since Kubernetes 1.31, with first-class
`HTTPRoute` and `GRPCRoute` resources. All three major implementations
(Envoy Gateway, Contour, Cilium) support `GRPCRoute` in production. SIG
Network released Ingress2Gateway 1.0 in March 2026 as the migration
assistant.

**Chart decision.** Gateway API is the default path; Ingress is a
transitional compatibility affordance.

- **`gateway.enabled=true`** (default): chart renders one `HTTPRoute` and
  one `GRPCRoute`, each `parentRefs`-ing into an operator-provided shared
  Gateway. `gateway.parentRefs` is required (list of Gateway references).
  `gateway.http.hostnames` and `gateway.grpc.hostnames` scope the routes.
  Chart does **not** render a `Gateway` resource (see Non-goals).
- **`ingress.enabled=true`** (off by default, transitional): chart renders
  two `Ingress` resources, one per protocol. The gRPC Ingress pre-seeds
  `annotations: { "nginx.ingress.kubernetes.io/backend-protocol": "GRPC" }`;
  operators on other controllers (Traefik, Kong, HAProxy) override.
  Separate hostnames per protocol (standard for dual-protocol deployments).
- **Mutually exclusive.** `values.schema.json` rejects both-enabled; if the
  schema is bypassed somehow, a chart `fail` template catches it.

**Reference topology documented in the chart README:**

```
          ┌─────────────────────────┐
          │  Cloudflare tunnel /    │
          │  external origin        │
          └────────────┬────────────┘
                       │
          ┌────────────▼────────────┐
          │  Gateway (platform ns)  │
          │  envoy-gateway / etc.   │
          └──┬───────────────────┬──┘
             │ HTTPRoute         │ GRPCRoute
             │                   │
       ┌─────▼─────┐       ┌─────▼─────┐
       │ Service   │       │ Service   │
       │ cyoda:    │       │ cyoda:    │
       │ http      │       │ grpc      │
       └─────┬─────┘       └─────┬─────┘
             │                   │
             └──────────┬────────┘
                        │
                   ┌────▼────┐
                   │ cyoda   │
                   │ pod(s)  │
                   └─────────┘
```

The README also documents the legacy Ingress topology for operators
mid-migration, with a pointer to Ingress2Gateway 1.0 for migration tooling.

### Observability

- **Probes:** liveness `/livez`, readiness `/readyz`, both on
  `containerPort metrics` (9091). Unconditional.
- **ServiceMonitor** (`monitoring.serviceMonitor.enabled=true`): renders a
  `monitoring.coreos.com/v1 ServiceMonitor` selecting port `metrics` on the
  `cyoda` Service. Standard kube-prometheus-stack pattern. Operators add
  selector labels via `monitoring.serviceMonitor.labels` so the
  Prometheus operator's `serviceMonitorSelector` picks it up.
- **Tracing / OTel:** delegated to `extraEnv`. No chart-owned OTel schema —
  the OTel spec evolves fast, and an open env injection is both simpler and
  more forward-compatible.

---

## 4. Configuration and secret management

### Four credentials

| Credential | Source | Projected-volume key | Env consumed |
|---|---|---|---|
| `CYODA_POSTGRES_URL` | operator `existingSecret` (required) | `dsn` | `CYODA_POSTGRES_URL_FILE` |
| `CYODA_JWT_SIGNING_KEY` | operator `existingSecret` (required) | `signing-key.pem` | `CYODA_JWT_SIGNING_KEY_FILE` |
| `CYODA_HMAC_SECRET` | operator `existingSecret` OR chart-generated | `secret` | `CYODA_HMAC_SECRET_FILE` |
| `CYODA_BOOTSTRAP_CLIENT_SECRET` | operator `existingSecret` OR chart-generated | `secret` | `CYODA_BOOTSTRAP_CLIENT_SECRET_FILE` |

### `_FILE` suffix support in the binary

A small config-loader change in the `app` package: a helper
`resolveSecretEnv(name string) string` that returns the value of the named
env var, OR reads `<name>_FILE` as a path and returns that file's contents
(with trailing whitespace trimmed — `TrimRight(data, "\n\r ")` is safe for
both DSN strings and multi-line PEM keys). Applied at the four credential
config sites; no other env vars get `_FILE` treatment (non-secrets don't
need it).

Precedence: `<name>_FILE` wins if both are set. Documented. Tested.

Failure modes:

- `<name>_FILE` set, file unreadable → fail fast with a clear error. No
  silent fallthrough to empty.
- `<name>_FILE` set to a file whose contents are empty (after trimming) →
  treated as unset. The normal downstream validation (e.g. "DSN required")
  reports the real problem.

### Auto-generation pattern for chart-managed Secrets

Used for `CYODA_HMAC_SECRET` and `CYODA_BOOTSTRAP_CLIENT_SECRET`. The
template logic:

```yaml
# templates/secret-hmac.yaml
{{- if not .Values.cluster.hmacSecret.existingSecret }}
{{- $name := printf "%s-hmac" (include "cyoda.fullname" .) }}
{{- $existing := (lookup "v1" "Secret" .Release.Namespace $name) }}
{{- $value := "" }}
{{- if $existing }}
{{- $value = index $existing.data "secret" }}
{{- else }}
{{- $value = randAlphaNum 48 | b64enc }}
{{- end }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ $name }}
  labels: {{- include "cyoda.labels" . | nindent 4 }}
  annotations:
    helm.sh/resource-policy: keep
type: Opaque
data:
  secret: {{ $value | quote }}
{{- end }}
```

**Properties:**

- **First install:** Secret doesn't exist → `randAlphaNum 48` generates a
  new value → Secret is created. 48 chars of alphanumeric at ~5.95 bits per
  char ≈ 285 bits of entropy; well above what either the HMAC or the
  bootstrap client secret needs.
- **Subsequent upgrades:** `lookup` finds the existing Secret → reuses its
  value → Secret stays stable across `helm upgrade` invocations.
- **`helm.sh/resource-policy: keep`:** `helm uninstall` does NOT delete the
  Secret. Reinstalling into the same namespace rejoins with the same HMAC,
  so the cluster doesn't lock itself out of previously-written data. If an
  operator truly wants a fresh start, they `kubectl delete secret` manually.

**Dry-run caveat.** `lookup` is a no-op on `helm template` and
`--dry-run`. The Secret renders with an empty value in those modes. This
is a known Helm limitation. Documented in the chart README. Operators doing
GitOps who need reproducible `helm template` output use the `existingSecret`
path for both chart-managed credentials.

**Bootstrap secret tightening (binary side).** The existing binary behavior
of auto-generating `CYODA_BOOTSTRAP_CLIENT_SECRET` when unset and printing
it to stdout is removed. Rationale: in a Kubernetes pod, that stdout print
goes into log aggregation (a small but real secret leak), and the
"generated-once, printed-once" UX is lost on rolling restarts anyway. New
behavior: in `jwt` mode, `CYODA_BOOTSTRAP_CLIENT_SECRET` is required
(startup fails fast if unset); in `mock` mode, ignored. Laptop users either
set it explicitly in their `.env` file or run `mock` mode. Chart users get
it via auto-generated Secret.

### ConfigMap / Secret split

Non-secret env vars go in a single ConfigMap referenced from the pod spec
via `envFrom: [{ configMapRef: { name: <fullname>-env } }]`. Sensitive
values never touch a ConfigMap; non-sensitive values never touch a Secret.

Example ConfigMap contents:

```yaml
CYODA_HTTP_PORT: "8080"
CYODA_GRPC_PORT: "9090"
CYODA_ADMIN_PORT: "9091"
CYODA_ADMIN_BIND_ADDRESS: "0.0.0.0"
CYODA_IAM_MODE: "jwt"
CYODA_REQUIRE_JWT: "true"
CYODA_STORAGE_BACKEND: "postgres"
CYODA_POSTGRES_AUTO_MIGRATE: "false"
CYODA_LOG_LEVEL: {{ .Values.logLevel | quote }}
CYODA_JWT_ISSUER: {{ .Values.jwt.issuer | quote }}
CYODA_JWT_EXPIRY_SECONDS: {{ .Values.jwt.expirySeconds | quote }}
CYODA_BOOTSTRAP_CLIENT_ID: {{ .Values.bootstrap.clientId | quote }}
CYODA_BOOTSTRAP_TENANT_ID: {{ .Values.bootstrap.tenantId | quote }}
CYODA_BOOTSTRAP_USER_ID: {{ .Values.bootstrap.userId | quote }}
CYODA_BOOTSTRAP_ROLES: {{ .Values.bootstrap.roles | quote }}
```

### values.yaml — top-level shape

```yaml
replicas: 1
logLevel: info

image:
  repository: ghcr.io/cyoda-platform/cyoda
  tag: ""                            # defaults to .Chart.AppVersion when empty
  pullPolicy: IfNotPresent

resources:
  requests: { cpu: 100m, memory: 256Mi }
  limits:   { cpu: 1000m, memory: 512Mi }

postgres:
  existingSecret: ""                 # REQUIRED — Secret must have key "dsn" containing full DSN

jwt:
  existingSecret: ""                 # REQUIRED — Secret must have key "signing-key.pem"
  issuer: cyoda
  expirySeconds: 3600

cluster:
  hmacSecret:
    existingSecret: ""               # OPTIONAL — chart auto-generates via lookup if unset

bootstrap:
  clientSecret:
    existingSecret: ""               # OPTIONAL — chart auto-generates via lookup if unset
  clientId: ""                       # auto-generated by binary if empty (existing behavior)
  tenantId: default-tenant
  userId: admin
  roles: "ROLE_ADMIN,ROLE_M2M"

extraEnv: []                         # operator injects OTel, feature flags, etc.

service:
  type: ClusterIP                    # only ClusterIP supported; Gateway/Ingress is the external path

gateway:
  enabled: true
  parentRefs: []                     # REQUIRED when enabled — list of Gateway references
  http:
    hostnames: []
  grpc:
    hostnames: []

ingress:
  enabled: false
  className: ""
  http:
    host: ""
    paths: [{ path: /, pathType: Prefix }]
    annotations: {}
    tls: []
  grpc:
    host: ""
    paths: [{ path: /, pathType: Prefix }]
    annotations:
      nginx.ingress.kubernetes.io/backend-protocol: GRPC
    tls: []

monitoring:
  serviceMonitor:
    enabled: false
    interval: 30s
    labels: {}

migrate:
  activeDeadlineSeconds: 600
  backoffLimit: 2
  resources:
    requests: { cpu: 100m, memory: 128Mi }
    limits:   { cpu: 500m, memory: 256Mi }

podDisruptionBudget:
  enabled: true                      # rendered only when replicas > 1
  minAvailable: 1

podAnnotations: {}
podLabels: {}
nodeSelector: {}
tolerations: []
affinity: {}
```

---

## 5. Migration flow

Three moving pieces, one end-to-end flow.

### Binary — `cyoda migrate` subcommand

New file `cmd/cyoda/migrate.go`, new dispatch case in `cmd/cyoda/main.go`:

```go
// main.go
case "migrate":
    os.Exit(runMigrate(os.Args[2:]))
```

`runMigrate` behavior:

- Parses `--timeout` flag (default 5 min) for the migration run.
- Loads the exact same env config the server does (via the same config loader
  — including the new `_FILE` suffix resolution from §4). No config
  duplication.
- Selects the backend based on `CYODA_STORAGE_BACKEND`. Memory and sqlite
  backends have no migrations; subcommand exits 0 with an info log. Postgres
  backend runs its migration logic.
- **Forward-only, idempotent.** Running twice is a no-op the second time
  (existing `applyMigrations` semantics in the Postgres plugin check
  `schema_version`).
- **Respects the schema-compatibility contract.** If DB schema is newer than
  code (downgrade rollback scenario), exits non-zero with the same error the
  server would produce. The migrate subcommand never silently allows a
  state where "migrations ran but they were backward migrations".
- **Exits cleanly.** No lingering goroutines, no admin listener, no
  background loops. Short-lived process.
- **Logs at info:** "migrations applied: N → M; duration: Xs".

Tested via `cmd/cyoda/migrate_test.go`:

- Unit: memory/sqlite backends no-op exit 0.
- E2E: against testcontainers Postgres — migrates a fresh DB; re-running is
  a no-op; artificially-advanced schema version triggers the compat refusal.

### Chart — migration Job

`templates/job-migrate.yaml` renders a Helm hook Job:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ include "cyoda.fullname" . }}-migrate-{{ .Release.Revision }}
  labels: {{- include "cyoda.labels" . | nindent 4 }}
  annotations:
    helm.sh/hook: pre-install,pre-upgrade
    helm.sh/hook-weight: "0"
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
spec:
  backoffLimit: {{ .Values.migrate.backoffLimit }}
  activeDeadlineSeconds: {{ .Values.migrate.activeDeadlineSeconds }}
  template:
    spec:
      restartPolicy: Never
      serviceAccountName: cyoda
      automountServiceAccountToken: false
      securityContext: { /* same hardening as the main StatefulSet pod */ }
      containers:
      - name: migrate
        image: ghcr.io/cyoda-platform/cyoda:{{ .Chart.AppVersion }}
        command: [/cyoda, migrate]
        envFrom: [{ configMapRef: { name: {{ include "cyoda.fullname" . }}-env } }]
        env:
        - name: CYODA_POSTGRES_URL_FILE
          value: /etc/cyoda/secrets/postgres-dsn
        volumeMounts:
        - name: secrets
          mountPath: /etc/cyoda/secrets
          readOnly: true
        resources: { /* .Values.migrate.resources */ }
      volumes:
      - name: secrets
        projected:
          defaultMode: 0400
          sources:
          - secret:
              name: {{ .Values.postgres.existingSecret }}
              items: [{ key: dsn, path: postgres-dsn }]
```

**Properties:**

- **Unique per revision** (`{{ .Release.Revision }}` in the name) — Helm
  revision numbers increment on every upgrade, so Job object names never
  collide.
- **`hook-delete-policy: before-hook-creation,hook-succeeded`** — successful
  Jobs clean up (no `kubectl get jobs` pile-up); failed Jobs are retained
  for `kubectl logs` postmortem.
- **Only mounts the DSN.** Principle of least privilege — the migration
  step doesn't need JWT keys, HMAC, or the bootstrap client secret.
- **Same hardened pod spec** as the main StatefulSet (non-root,
  `readOnlyRootFilesystem`, all capabilities dropped).
- **`backoffLimit: 2`, `activeDeadlineSeconds: 600` (10 min)** defaults.
  Operators running large migrations override via
  `migrate.activeDeadlineSeconds`.

### End-to-end rollout

`helm upgrade cyoda cyoda/cyoda --version 0.2.0` (new chart ships with a
new `appVersion` whose image includes a schema change):

1. Helm renders manifests, including the new
   `cyoda-migrate-<new-revision>` Job.
2. Helm creates the Job. Job pod pulls the new image, runs `cyoda migrate`,
   applies missing migrations, exits 0.
3. Helm sees Job success; applies the StatefulSet update.
4. StatefulSet rolls pods. Each new pod starts with `AUTO_MIGRATE=false`
   and its schema-compat check passes because migrations already ran.
5. Rolling completes. Old pods terminate. Release succeeds.

### Failure modes

- **Migration fails (new schema has a bug):** Job exits non-zero after
  `backoffLimit` retries → `helm upgrade` fails → Helm's rollback logic
  restores the prior release's values → StatefulSet untouched → old pods
  keep serving. Operator `kubectl logs job/cyoda-migrate-<rev>` diagnoses.
- **Migration takes longer than `activeDeadlineSeconds`:** Job killed →
  same as failure path. Surfaces "this migration is slow" loudly.
- **Schema newer than code (downgrade):** `cyoda migrate` refuses (by
  design) → Job fails → `helm upgrade` fails → old release stays in place.

### Rollback and downgrade explicitly out of scope

cyoda-go Postgres migrations are forward-only. An operator trying to
downgrade the chart to an older `appVersion` that expects an older schema
will hit: migrate Job refuses → `helm upgrade` fails → old release
preserved. If they force past that (e.g. `helm rollback`), the old binaries
will hit the schema-compat refusal on startup and crash-loop. **Recovery
requires a DB restore from backup.** Chart README documents this explicitly;
it's not something the Helm layer can or should fix. This matches the
shared-spec fail-closed contract.

---

## 6. Release mechanics

Two GitHub Actions workflows, both currently pre-stubs in the repo, start
doing real work when this deliverable lands.

### `release-chart.yml` — publish chart

**Trigger:** tag push matching `cyoda-*` (e.g. `cyoda-0.1.0`).

**Steps:**

1. Checkout at the tag.
2. Verify `deploy/helm/cyoda/Chart.yaml` exists (fails the release otherwise
   — already scaffolded).
3. Verify `Chart.yaml` `version:` matches the tag suffix
   (`cyoda-0.2.0` requires `version: 0.2.0`). Prevents mis-tagged releases.
4. `helm lint deploy/helm/cyoda`.
5. `helm template` + `kubeconform` (same checks as CI's layer 1 — see §7).
6. `helm/chart-releaser-action@v1` packages the chart and publishes to the
   `gh-pages` branch as a Helm repository (`index.yaml` + `<name>-<version>.tgz`).
7. The .tgz is also uploaded to the GitHub Release matching the tag, so
   `helm pull` against the Release asset works as an alternative.

**Published URL:** `https://cyoda-platform.github.io/cyoda-go` (GitHub
Pages, served from the `gh-pages` branch).

### `bump-chart-appversion.yml` — keep appVersion aligned with binary

**Trigger:** tag push matching `v*` non-prerelease (e.g. `v0.2.0`, not
`v0.2.0-rc.1`).

**Steps:**

1. Checkout `main` in a new branch `chore/bump-chart-appversion-<version>`.
2. `sed` the `appVersion:` field in `deploy/helm/cyoda/Chart.yaml` to match
   the binary tag.
3. Does NOT touch chart `version:` — that's an independent semver (chart
   changes ≠ app changes).
4. Opens a PR against `main` with a description referencing the binary
   release notes.
5. Does NOT auto-merge. A human reviews and decides whether the appVersion
   bump warrants a chart release (if so, they also bump chart `version` in
   the same PR and tag the chart after merge).

This separation — binary releases and chart releases are independent tag
series with their own semver — is the standard pattern
(ingress-nginx, Grafana, cert-manager all do this). Chart bug fixes ship
without binary bumps; binary bumps ship without chart changes.

### Chart versioning — v0.1.0 start

- **Chart version:** `0.1.0`. First published chart. Going forward:
  - Patch (`0.1.1`): template bugfix, no values schema changes.
  - Minor (`0.2.0`): additive values schema change, new optional feature.
  - Major (`1.0.0`): commit to stable values schema; breaking changes
    require a major.
- **appVersion:** concrete binary tag at chart-ship time. Tracks the binary
  via `bump-chart-appversion.yml` PRs.

### Operator consumption

```bash
# Add the repo
helm repo add cyoda https://cyoda-platform.github.io/cyoda-go
helm repo update

# Install
helm install cyoda cyoda/cyoda -f my-values.yaml

# Pin to a specific chart version
helm install cyoda cyoda/cyoda --version 0.1.0 -f my-values.yaml

# Upgrade (migration Job runs automatically pre-upgrade)
helm upgrade cyoda cyoda/cyoda --version 0.2.0 -f my-values.yaml
```

OCI-based chart publishing (to `ghcr.io`) was considered and rejected for
v0.1: `helm search repo` doesn't work against OCI registries, and
chart-releaser is the more-traveled path that most mainstream charts still
use. OCI publishing is a future-version consideration, not a current need.

---

## 7. Testing and CI

### Layer 1 — lint + template + validate (every chart-affecting PR)

New workflow `.github/workflows/helm-chart-ci.yml`. Triggered on PRs that
touch `deploy/helm/**` or the workflow file itself; also runs on pushes to
`main`.

**Job `helm-lint-and-validate` steps:**

1. Checkout, install Helm v3.16.
2. Install `kubeconform` v0.6.7.
3. `helm lint deploy/helm/cyoda`.
4. `helm template cyoda deploy/helm/cyoda` with **three overlays**, each
   piped into `kubeconform -strict -kubernetes-version 1.31.0` with
   Gateway API 1.2 schemas:
   - Default (single replica, Gateway API enabled).
   - Ingress-mode override (`gateway.enabled=false`, `ingress.enabled=true`).
   - Three-replica cluster-mode (`replicas=3`, Gateway enabled).
5. Every overlay provides fixture secrets via `--set postgres.existingSecret=test-dsn`
   and `--set jwt.existingSecret=test-jwt` (the Secret objects don't need to
   exist at template-time; we're only rendering, not installing).

Runtime: ~30 seconds. Catches template syntax errors, missing-required-values
failures, invalid Kubernetes object schemas, Gateway API schema violations.

### Layer 2 — `ct install` on kind (chart-affecting PRs + main)

**Job `helm-install-smoke` steps** (runs after layer 1 passes):

1. Checkout.
2. `helm/kind-action@v1` creates a kind cluster, Kubernetes v1.31.0.
3. Install Gateway API v1.2 CRDs from the upstream manifest.
4. Install Envoy Gateway via its official Helm chart (one-liner; ~60s
   wait). Chosen as the reference Gateway implementation for CI because it
   has full `HTTPRoute`/`GRPCRoute` support with no known scale caveats.
5. Create a Postgres sidecar pod (postgres:16-alpine) with a fixed
   password and `cyoda` database.
6. Generate fixture material at CI-runtime and create Secrets:
   - Generate an ephemeral RSA private key with `openssl genrsa -out
     /tmp/test-jwt.pem 2048`. The key lives only for the CI job — no
     committed private key in the repo.
   - `test-dsn` Secret containing the Postgres DSN against the sidecar.
   - `test-jwt` Secret containing the generated key under
     `signing-key.pem`.
7. `helm/chart-testing-action@v2` `ct install` — installs the chart, waits
   for `--timeout 5m`, uninstalls, re-installs (exercises the
   `lookup`-based chart-managed Secret pattern on re-install).
8. Port-forward the `cyoda` Service's `metrics` port; `curl -fsS
   http://localhost:9091/readyz` to confirm end-to-end readiness.
9. `helm test cyoda` — runs the chart's own `test-readyz` hook pod, which
   curls `/readyz` via in-cluster DNS.

Runtime: ~2.5 minutes. Catches real install failures the layer-1 schema
check can't see: Secret-wiring mismatches, probe path misses, migration Job
failures, `envFrom` references that don't resolve, Gateway API schema
issues `kubeconform` misses.

### Out of layer 2 — followed up explicitly

- **Upgrade testing.** Requires two chart versions with a schema diff.
  Filed as §8-F2.
- **Multi-replica cluster-mode gossip coordination.** Requires substantial
  extra infra (3-pod wait, gossip peer-count assertion via logs or admin
  endpoint). Filed as §8-F1.

### Test fixtures

- **No committed private keys.** The CI job generates an ephemeral RSA key
  inline via `openssl genrsa` at runtime. No `test-jwt.pem` or equivalent
  lives in the repo — this avoids the "committed dev-key that might end up
  in a non-CI context" concern raised in the security rules.
- `.github/ct.yaml` — chart-testing config:
  ```yaml
  chart-dirs: [deploy/helm]
  charts: [cyoda]
  validate-maintainers: false
  helm-extra-args: "--wait --timeout 5m"
  target-branch: main
  ```

### Integration with existing CI

- Existing `ci.yml` (Go tests) — no change.
- Existing `per-module-hygiene` job (plugin submodule builds) — no change.
- New `helm-chart-ci.yml` runs independently, does not block non-chart PRs.
- `release-chart.yml` and `bump-chart-appversion.yml` pre-stubs become
  operative once chart files land.

---

## 8. Follow-up issues

Filed as GitHub issues on PR merge. These are deferred with clear reasons —
never buried as TODOs in code.

| ID | Title | Reason deferred | Acceptance criteria |
|---|---|---|---|
| F1 | Layer 3 CI: multi-replica cluster-mode install with gossip coordination | Requires substantial extra test infrastructure; value is real but independent from the v0.1 baseline | CI job installs chart at `replicas=3, gateway.enabled=true`, verifies 3 pods reach Ready, verifies `gossip.go` logs "cluster of 3", tears down cleanly. Runs on main + nightly. |
| F2 | `helm upgrade` migration-path testing | Needs two chart versions with a schema diff; impossible at v0.1 in isolation | When v0.2 ships with a schema change, add CI: install v0.1.0, upgrade to v0.2.0, verify migration Job ran and new pods serve traffic. |
| F3 | Ingress2Gateway migration guide | Operator-facing documentation deliverable, not chart code | `deploy/helm/cyoda/docs/migrating-from-ingress.md` walks `ingress.enabled=true` → `gateway.enabled=true` via Ingress2Gateway 1.0. |
| F4 | Gateway API PolicyAttachment patterns (rate limiting, auth filters) | Specific to each operator's Gateway controller; not chart-universal | Document recommended `BackendTrafficPolicy` / `SecurityPolicy` overlays for Envoy Gateway; do not render from chart. |
| F5 | Chart v0.2+ optional features | Not needed for the v0.1 baseline; each is a separable increment | Each feature (HPA, NetworkPolicy, PodMonitor alternative, external-secrets-operator integration) becomes its own minor chart version with its own values schema addition and tests. |
| F6 | NetworkPolicy example restricting admin port ingress | Shipped in Docker compose README as a caveat; the Helm `CYODA_ADMIN_BIND_ADDRESS=0.0.0.0` when ServiceMonitor is enabled creates the same operational concern | Chart README documents a `NetworkPolicy` example restricting admin-port ingress to prometheus-operator namespace. |

Picked up by this deliverable from existing context:

- `_FILE` suffix support for credentials — closed by §4 + §1 binary changes.
- Plugin submodule test coverage aggregator (existing issue #46) — not
  touched by this deliverable; stays open.

---

## 9. Summary of decisions

For quick cross-reference against the clarifying-question trail:

| Area | Decision |
|---|---|
| Migration mode | New `cyoda migrate` subcommand |
| DB subchart | None. Chart has zero dependencies. DSN required via `existingSecret`. |
| DSN secret wiring | `existingSecret` only; never plaintext in values |
| JWT key wiring | `existingSecret`, required. `_FILE` suffix convention applied uniformly. |
| `_FILE` suffix scope | All four credential-shaped env vars. Single `resolveSecretEnv` helper. |
| Migration Job hooks | `pre-install,pre-upgrade`, blocking. 10-min default `activeDeadlineSeconds`. |
| Workload kind | StatefulSet always. Cluster mode always on. Scale via `replicas` alone. |
| HMAC secret | Auto-generated via `lookup` + `randAlphaNum 48`, or operator `existingSecret`. `resource-policy: keep`. |
| Bootstrap client secret | Same pattern. Binary: stdout-print removed; jwt mode requires it. |
| Service shape | Single ClusterIP `cyoda` with named ports `http/grpc/metrics` + headless `cyoda-headless` for gossip |
| Routing | Gateway API default (`HTTPRoute` + `GRPCRoute`, parentRefs required); Ingress transitional |
| Gateway resource rendering | No. Shared platform Gateway expected; chart renders only routes. |
| Observability | Unconditional probes + optional ServiceMonitor + open `extraEnv` for OTel |
| Pod security | Non-root 65532, readOnlyRootFilesystem, all caps dropped, seccomp RuntimeDefault, SA token not mounted |
| Storage backend | Postgres only. ConfigMap hardcodes it; no user-facing values knob. |
| Persistence | None. `volumeClaimTemplates: []`. |
| Chart publishing | `helm/chart-releaser-action` → GitHub Pages |
| CI | Layers 1 (every PR) + 2 (chart-affecting PRs + main). Layer 3 filed as F1. |
