-- Drops every table the schema creates, and the migration records, so the
-- database is empty again: customer data, the tax tables, everything.
--
--   psql "$DATABASE_URL" -1 -f db/sql/drop.sql
--
-- Then db/sql/schema.sql (or start the server, which builds it), then
-- default-data.sql and, on development databases, test-data.sql.
--
-- DESTRUCTIVE: never run this against a database real customers use.

DROP TABLE IF EXISTS
    deletions,
    snapshots,
    scenarios,
    profiles,
    items,
    account_profile,
    sessions,
    customer_identities,
    customers,
    tax_zip_ranges,
    tax_layers,
    tax_jurisdictions,
    payroll_tax_years,
    capital_gains_tax_years,
    tax_data_version,
    schema_migrations
CASCADE;
