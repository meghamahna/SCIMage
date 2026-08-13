-- Lets a test simulate an audit-write failure without taking a table-wide
-- lock the way `ALTER TABLE ... RENAME` does. The trigger is a no-op for
-- every ordinary write: current_setting's missing_ok argument returns NULL
-- for any session that never sets this GUC, and NULL = 'true' is not true,
-- so nothing changes for the running server or any other test.
CREATE OR REPLACE FUNCTION audit_log_fault_injection() RETURNS trigger AS $$
BEGIN
  IF current_setting('scimage.simulate_audit_failure', true) = 'true' THEN
    RAISE EXCEPTION 'simulated audit log failure';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_log_fault_trigger
  BEFORE INSERT ON audit_log
  FOR EACH ROW EXECUTE FUNCTION audit_log_fault_injection();
