---
topic: config.auth
title: "auth configuration"
stability: stable
see_also:
  - config
  - run
---

# config.auth

## NAME

config.auth — IAM mode, JWT issuer, HMAC secret, and admin bootstrap controls.

## SYNOPSIS

cyoda supports two IAM modes: `mock` (development) and `jwt` (production). Configure the
mode via `CYODA_IAM_MODE`. Use `CYODA_REQUIRE_JWT` as a production safety guard to refuse
startup unless JWT mode is properly configured.

## OPTIONS

### IAM mode

- `CYODA_IAM_MODE` — authentication mode: `mock` or `jwt` (default: `mock`)
- `CYODA_REQUIRE_JWT` — refuse to start unless `jwt` mode is active and a signing key is set
  (default: `false`)

### Mock mode (`CYODA_IAM_MODE=mock`)

- `CYODA_IAM_MOCK_ROLES` — comma-separated default user roles assigned to all requests
  in mock mode (default: `ROLE_ADMIN,ROLE_M2M`)

### JWT mode (`CYODA_IAM_MODE=jwt`)

- `CYODA_JWT_SIGNING_KEY` — RSA private key in PEM format; required in jwt mode
- `CYODA_JWT_SIGNING_KEY_FILE` — file path for `CYODA_JWT_SIGNING_KEY` (takes precedence)
- `CYODA_JWT_ISSUER` — JWT issuer claim (`iss`) (default: `cyoda`)
- `CYODA_JWT_AUDIENCE` — required audience claim (`aud`) on inbound JWTs;
  empty string disables the audience check (default: empty)
- `CYODA_JWT_EXPIRY_SECONDS` — token lifetime in seconds (default: `3600`)

### HMAC secret (inter-node dispatch authentication)

- `CYODA_HMAC_SECRET` — hex-encoded HMAC secret for inter-node dispatch auth
- `CYODA_HMAC_SECRET_FILE` — file path for `CYODA_HMAC_SECRET` (takes precedence)

### Bootstrap M2M client

cyoda can provision a machine-to-machine client at startup for automation and CI.

- `CYODA_BOOTSTRAP_CLIENT_ID` — bootstrap M2M client ID (optional)
- `CYODA_BOOTSTRAP_CLIENT_SECRET` — bootstrap M2M client secret; must be set when
  `CYODA_BOOTSTRAP_CLIENT_ID` is set (and vice versa)
- `CYODA_BOOTSTRAP_CLIENT_SECRET_FILE` — file path for `CYODA_BOOTSTRAP_CLIENT_SECRET`
  (takes precedence)
- `CYODA_BOOTSTRAP_TENANT_ID` — tenant for the bootstrap client (default: `default-tenant`)
- `CYODA_BOOTSTRAP_USER_ID` — user ID for the bootstrap client (default: `admin`)
- `CYODA_BOOTSTRAP_ROLES` — comma-separated roles granted to the bootstrap client
  (default: `ROLE_ADMIN,ROLE_M2M`)

## EXAMPLES

**Development (mock auth):**

```
CYODA_IAM_MODE=mock
CYODA_IAM_MOCK_ROLES=ROLE_ADMIN,ROLE_M2M
```

**Production (JWT auth):**

```
CYODA_IAM_MODE=jwt
CYODA_REQUIRE_JWT=true
CYODA_JWT_SIGNING_KEY_FILE=/etc/secrets/signing.pem
CYODA_JWT_ISSUER=https://auth.example.com
CYODA_JWT_AUDIENCE=cyoda-api
CYODA_JWT_EXPIRY_SECONDS=3600
```

**With bootstrap client:**

```
CYODA_BOOTSTRAP_CLIENT_ID=ci-client
CYODA_BOOTSTRAP_CLIENT_SECRET_FILE=/etc/secrets/ci-secret
CYODA_BOOTSTRAP_ROLES=ROLE_ADMIN,ROLE_M2M
```

## SEE ALSO

- config
- run
