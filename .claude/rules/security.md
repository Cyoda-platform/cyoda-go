# Security Checklist

Apply to all code — no exceptions.

- **Credentials:** Never log, print to stdout, include in error messages, or store outside designated secure locations (`.env` files that are gitignored). Keep in memory only for the duration needed. Never serialize to persistent storage.
- **Tenant isolation:** Every data path must be verified for cross-tenant leakage. No tenant can access, modify, or infer the existence of another tenant's data.
- **Input validation:** Validate user-supplied input at system boundaries. Reject malformed input early.
- **Output sanitization:** Responses and logs must not leak internal state, stack traces, connection strings, or credentials to callers.
