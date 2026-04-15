-- Remove FORCE ROW LEVEL SECURITY from all tables.
-- RLS policies remain defined but are not enforced for the table owner.
-- Enforcement activates when:
--   (a) the application connects as a non-owner role (production), or
--   (b) SET LOCAL app.current_tenant is called inside a transaction (Plan 5)
-- Application-level WHERE tenant_id = $1 is the primary isolation mechanism.

ALTER TABLE entities NO FORCE ROW LEVEL SECURITY;
ALTER TABLE entity_versions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE sm_audit_events NO FORCE ROW LEVEL SECURITY;
ALTER TABLE models NO FORCE ROW LEVEL SECURITY;
ALTER TABLE kv_store NO FORCE ROW LEVEL SECURITY;
ALTER TABLE messages NO FORCE ROW LEVEL SECURITY;
