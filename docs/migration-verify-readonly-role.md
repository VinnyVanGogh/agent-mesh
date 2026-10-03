# Migration Verify: Read-Only DB Connection

StayPoint can automatically verify a migration was applied by connecting to your
Postgres database with a **read-only** role. No writes are ever issued.

## 1. Create the read-only role

Run this once in your Postgres database (e.g. via the Supabase SQL editor):

```sql
-- Create a dedicated read-only role for StayPoint migration verification.
CREATE ROLE staypoint_readonly WITH LOGIN PASSWORD 'choose_a_strong_password';

-- Grant read access to information_schema and pg_catalog (already public, but
-- grant explicit USAGE on target schemas you need to verify):
GRANT USAGE ON SCHEMA public TO staypoint_readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO staypoint_readonly;

-- Ensure future tables are also accessible:
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT ON TABLES TO staypoint_readonly;
```

> **Security note**: `staypoint_readonly` only needs `USAGE` on the schema and
> `SELECT` on the catalog views (`information_schema.columns`, `pg_policies`,
> `pg_indexes`, `pg_proc`, `pg_trigger`, `pg_class`). These are all readable with
> the permissions above.

## 2. Store the connection string in the macOS Keychain

StayPoint reads the DSN from the macOS Keychain under service name
`staypoint-readonly-db`, with the account set to your project's repo path.
Store it like this (replace values accordingly):

```bash
security add-generic-password \
  -s staypoint-readonly-db \
  -a "/path/to/your/project" \
  -w "postgresql://staypoint_readonly:choose_a_strong_password@db.xxxx.supabase.co:5432/postgres?sslmode=require"
```

Or use the Settings panel in StayPoint (coming soon) to configure it via the UI.

## 3. How verification works

When you click **Mark applied** on a migration file:

1. StayPoint parses the SQL DDL/DML and generates read-only checks.
2. It reads the DSN from the Keychain.
3. It opens a connection and runs:
   ```sql
   BEGIN READ ONLY;
   SET LOCAL statement_timeout = '5000ms';
   -- … SELECT … AS ok for each object
   ROLLBACK;
   ```
4. Every check must return `true`. Any failure shows which objects are missing.
5. Only when all pass is the migration recorded as **Applied** in the activity log.

## 4. Manual fallback

If no connection is configured, StayPoint shows a **Copy verify query** button.
Run the query in your SQL editor (e.g. Supabase SQL editor) and paste the
results back, or tick each object individually.

## 5. Override

If you must mark a migration applied despite failing checks (e.g. the migration
was applied outside StayPoint), supply an **Override reason**. The reason is
recorded in the activity log.

## 6. Ship Review gate

The **Approve & merge** button in the Ship Review card is blocked while any
migration file in the task's diff has not been verified. You must either:
- Verify all migrations via auto or manual mode, or
- Supply a `migration_override_reason` in the Approve request.
