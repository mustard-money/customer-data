-- Removes the test accounts added by test-data.sql (test0001@mustard.money
-- to test0100@mustard.money) and everything they saved: deleting a customer
-- cascades to their sessions, sign-in identities, items, household,
-- scenarios, profile and deletion records.
--
--   make test-accounts-remove     (or: psql "$DATABASE_URL" -f db/sql/test-data-remove.sql)
--
-- Matches both the id and the email the seed gives them, so an account that
-- signed up through the app with one of these addresses (a random id) is left alone.

BEGIN;

DELETE FROM customers
WHERE id ~ '^test[0-9]{4}$'
  AND email ~ '^test[0-9]{4}@mustard\.money$'
  AND substring(id from 5 for 4) = substring(email from 5 for 4);

COMMIT;
