-- 100 test accounts for development and testing: test0001@mustard.money to
-- test0100@mustard.money, all with the password "mustard". Each starts empty
-- (no items, household, scenarios or profile).
--
-- DEVELOPMENT AND TEST DATABASES ONLY. These accounts share a known, short
-- password; never load this into a database real customers use.
--
--   make test-accounts            (or: psql "$DATABASE_URL" -f db/sql/test-data.sql)
--   make test-accounts-remove     removes them again, with everything they saved
--                                 (db/sql/test-data-remove.sql)
--
-- Run it after the schema exists (schema.sql, or the server's startup).
-- Running it again is safe: accounts that already exist are left as they are.
--
-- The password is checked only at sign-in (the 8-character minimum applies
-- to sign-up), so "mustard" works for these accounts. Postgres can't compute
-- argon2id, so the hash below was made with the server's own parameters
-- (auth/password.go: argon2id, m=19456 KiB, t=2, p=1, 32-byte key); all 100
-- accounts share it.

BEGIN;

INSERT INTO customers (id, email, password_hash)
SELECT
    'test' || lpad(n::text, 4, '0'),
    'test' || lpad(n::text, 4, '0') || '@mustard.money',
    '$argon2id$v=19$m=19456,t=2,p=1$MlWiXTGBBozeyKP//qX65Q$HzEInNZoAb/xpguc8dZrusGf24LC/Mrp+eGuJfvDYeE'
FROM generate_series(1, 100) AS n
ON CONFLICT DO NOTHING;

COMMIT;
