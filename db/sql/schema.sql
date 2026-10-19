-- The whole database schema: what migrations 0001–0032 built, in one file.
--
-- The server applies it to an empty database at startup and records it in
-- schema_migrations as 0032_baseline; a database that already ran those
-- migrations is left as it is. Later changes go in db/migrations/0033_*.sql
-- onward, never here: this file stays the 0032 baseline.
--
-- By hand, after db/sql/drop.sql and before default-data.sql and
-- test-data.sql:  psql "$DATABASE_URL" -1 -f db/sql/schema.sql
--
-- Columns keep the order the migrations added them in, so a database built
-- from this file matches one built by the migrations (pg_dump shows the same).

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Customers, their sign-in methods and their sessions. A customer signs in
-- with a password (password_hash set), with Apple/Google (a row in
-- customer_identities), or both. Emails are stored lowercased by the
-- application and are unique when present.
CREATE TABLE customers (
    id            text        PRIMARY KEY
                              CONSTRAINT customers_id_format CHECK (id ~ '^[A-Za-z0-9_-]{1,64}$'),
    email         text        UNIQUE
                              CONSTRAINT customers_email_format CHECK (
                                  char_length(email) <= 254 AND email = lower(email)),
    password_hash text,
    created_at    timestamptz NOT NULL DEFAULT date_trunc('milliseconds', now())
);

-- An external identity (Apple or Google account) linked to a customer, keyed
-- by the provider's stable subject id.
CREATE TABLE customer_identities (
    provider    text        NOT NULL
                            CONSTRAINT customer_identities_provider_valid CHECK (provider IN ('apple', 'google')),
    subject     text        NOT NULL
                            CONSTRAINT customer_identities_subject_length CHECK (char_length(subject) BETWEEN 1 AND 255),
    customer_id text        NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject)
);

CREATE INDEX customer_identities_customer_id_idx ON customer_identities (customer_id);

-- Bearer sessions. Only the SHA-256 of the token is stored.
CREATE TABLE sessions (
    token_hash  bytea       PRIMARY KEY
                            CONSTRAINT sessions_token_hash_length CHECK (octet_length(token_hash) = 32),
    customer_id text        NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

CREATE INDEX sessions_customer_id_idx ON sessions (customer_id);

-- ---------------------------------------------------------------------------
-- Income, investment and expense items synced from the app, per customer.
-- Ids only for scenario_id, replaces and workplace.incomeId: items and
-- scenarios sync independently, so these aren't foreign keys. jsonb columns
-- (benefits, loan, workplace, tax_exempt) are validated by the application.
CREATE TABLE items (
    id          text          NOT NULL
                              CONSTRAINT items_id_format CHECK (id ~ '^[A-Za-z0-9_-]{1,64}$'),
    category    text          NOT NULL
                              CONSTRAINT items_category_valid CHECK (category IN ('income', 'investments', 'expenses')),
    name        text          NOT NULL
                              CONSTRAINT items_name_length CHECK (char_length(name) BETWEEN 1 AND 200),
    amount      numeric(14,2) NOT NULL
                              CONSTRAINT items_amount_nonnegative CHECK (amount >= 0),
    -- Income can't be one-time or daily; expenses recur daily, weekly, monthly
    -- or yearly, or happen once (in their start month, with no end).
    frequency   text
                              CONSTRAINT items_frequency_valid CHECK (
                                  frequency IN ('once', 'daily', 'weekly', 'biweekly', 'semimonthly', 'monthly',
                                                'quarterly', 'semiannually', 'yearly')),
    created_at  timestamptz   NOT NULL,
    updated_at  timestamptz   NOT NULL DEFAULT now(),
    customer_id text          NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    -- Investments: the kind of account.
    account     text
                              CONSTRAINT items_account_valid CHECK (account IN (
                                  'taxable', 'bank', 'cd', 'hsa', 'traditionalIra', 'rothIra',
                                  'traditional401k', 'roth401k', 'traditional403b', 'roth403b',
                                  'traditional457b', 'roth457b'
                              )),
    -- Income: the tax form it's reported on.
    income_type text
                              CONSTRAINT items_income_type_valid CHECK (income_type IN (
                                  'w2', '1099nec', '1099misc', '1099k', 'k1', '1099int',
                                  '1099div', '1099b', '1099r', 'ssa1099', '1099g', 'other'
                              )),
    -- W-2 income: workplace savings (401(k)/403(b) with match, 457(b), HSA).
    benefits    jsonb,
    -- Investments: when the value was measured, and their own yearly return.
    as_of_year  smallint
                              CONSTRAINT items_as_of_year_min CHECK (as_of_year >= 1900),
    as_of_month smallint
                              CONSTRAINT items_as_of_month_range CHECK (as_of_month BETWEEN 1 AND 12),
    return_rate double precision
                              CONSTRAINT items_return_rate_range CHECK (return_rate BETWEEN -0.5 AND 0.5),
    -- Income and expenses: when they start and end.
    start_year  smallint      CONSTRAINT items_start_year_range CHECK (start_year BETWEEN 1900 AND 2150),
    start_month smallint      CONSTRAINT items_start_month_range CHECK (start_month BETWEEN 1 AND 12),
    end_year    smallint      CONSTRAINT items_end_year_range CHECK (end_year BETWEEN 1900 AND 2150),
    end_month   smallint      CONSTRAINT items_end_month_range CHECK (end_month BETWEEN 1 AND 12),
    -- Retirement account investments: a withdrawal plan (start and yearly share).
    withdrawal_start_year  smallint
                              CONSTRAINT items_withdrawal_start_year_range CHECK (withdrawal_start_year BETWEEN 1900 AND 2150),
    withdrawal_start_month smallint
                              CONSTRAINT items_withdrawal_start_month_range CHECK (withdrawal_start_month BETWEEN 1 AND 12),
    withdrawal_rate        double precision
                              CONSTRAINT items_withdrawal_rate_range CHECK (withdrawal_rate > 0 AND withdrawal_rate <= 1),
    -- Monthly expenses: the loan this is the payment on.
    loan        jsonb,
    -- Scenarios as layers over Main: the scenario the item belongs to, and
    -- the Main item it stands in for there.
    scenario_id text          CONSTRAINT items_scenario_id_format CHECK (scenario_id ~ '^[A-Za-z0-9_-]{1,64}$'),
    replaces    text          CONSTRAINT items_replaces_format CHECK (replaces ~ '^[A-Za-z0-9_-]{1,64}$'),
    -- Investments: the W-2 income whose workplace plan this is ({"incomeId", "plan"}).
    workplace   jsonb         CONSTRAINT items_workplace_investments CHECK (
                                  workplace IS NULL OR (category = 'investments' AND jsonb_typeof(workplace) = 'object')),
    -- Income marked self-employment (stored only when true).
    self_employment boolean
                              CONSTRAINT items_self_employment_income_only CHECK (
                                  self_employment IS NULL OR (self_employment AND category = 'income')),
    -- 1099-DIV income that isn't qualified dividends (stored only when false).
    qualified_dividends boolean
                              CONSTRAINT items_qualified_dividends_1099div_only CHECK (
                                  qualified_dividends IS NULL OR (NOT qualified_dividends AND income_type = '1099div')),
    -- Brokerage investments held as fixed income (null means equities): the
    -- yearly income rate and the levels of tax it's exempt from.
    asset_class text
                              CONSTRAINT items_asset_class_valid CHECK (
                                  asset_class IS NULL OR (asset_class = 'fixedIncome' AND account = 'taxable')),
    income_rate double precision
                              CONSTRAINT items_income_rate_fixed_income CHECK (
                                  (income_rate IS NULL) = (asset_class IS NULL)
                                  AND (income_rate IS NULL OR income_rate BETWEEN 0 AND 0.5)),
    tax_exempt  jsonb
                              CONSTRAINT items_tax_exempt_fixed_income CHECK (tax_exempt IS NULL OR asset_class IS NOT NULL),
    PRIMARY KEY (customer_id, id),
    CONSTRAINT items_frequency_by_category CHECK (
        (category = 'investments' AND frequency IS NULL)
        OR (category <> 'investments' AND frequency IS NOT NULL)
    ),
    CONSTRAINT items_expense_frequency CHECK (
        category <> 'expenses' OR frequency IN ('once', 'daily', 'weekly', 'monthly', 'yearly')
    ),
    CONSTRAINT items_account_investments_only CHECK (category = 'investments' OR account IS NULL),
    CONSTRAINT items_income_type_income_only CHECK (category = 'income' OR income_type IS NULL),
    CONSTRAINT items_benefits_w2_only CHECK (benefits IS NULL OR (category = 'income' AND income_type = 'w2')),
    CONSTRAINT items_as_of_month_needs_year CHECK (as_of_month IS NULL OR as_of_year IS NOT NULL),
    CONSTRAINT items_investment_fields_only CHECK (
        category = 'investments' OR (as_of_year IS NULL AND return_rate IS NULL)
    ),
    CONSTRAINT items_months_need_years CHECK (
        (start_month IS NULL OR start_year IS NOT NULL) AND (end_month IS NULL OR end_year IS NOT NULL)
    ),
    CONSTRAINT items_withdrawals_complete CHECK (
        (withdrawal_start_year IS NULL) = (withdrawal_rate IS NULL)
        AND (withdrawal_start_month IS NULL OR withdrawal_start_year IS NOT NULL)
    ),
    CONSTRAINT items_withdrawals_retirement_only CHECK (
        withdrawal_rate IS NULL OR (category = 'investments' AND account IN (
            'traditionalIra', 'rothIra', 'traditional401k', 'roth401k',
            'traditional403b', 'roth403b', 'traditional457b', 'roth457b'
        ))
    ),
    CONSTRAINT items_income_frequency CHECK (category <> 'income' OR frequency NOT IN ('once', 'daily')),
    CONSTRAINT items_dates_not_investments CHECK (
        category <> 'investments' OR (start_year IS NULL AND end_year IS NULL)
    ),
    CONSTRAINT items_once_no_end CHECK (frequency IS DISTINCT FROM 'once' OR end_year IS NULL),
    CONSTRAINT items_loan_monthly_expenses_only CHECK (
        loan IS NULL OR (category = 'expenses' AND frequency = 'monthly')
    ),
    CONSTRAINT items_replaces_needs_scenario CHECK (replaces IS NULL OR scenario_id IS NOT NULL)
);

CREATE INDEX items_customer_created_at_idx ON items (customer_id, created_at, id);

-- Household members.
CREATE TABLE profiles (
    id           text        NOT NULL
                             CONSTRAINT profiles_id_format CHECK (id ~ '^[A-Za-z0-9_-]{1,64}$'),
    name         text        NOT NULL
                             CONSTRAINT profiles_name_length CHECK (char_length(name) BETWEEN 1 AND 200),
    relationship text        NOT NULL
                             CONSTRAINT profiles_relationship_valid CHECK (relationship IN ('self', 'partner', 'child', 'other')),
    -- "Not in the future" is enforced by the application; CHECKs must not
    -- depend on the clock.
    birth_year   integer
                             CONSTRAINT profiles_birth_year_min CHECK (birth_year >= 1900),
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    birth_month  smallint
                             CONSTRAINT profiles_birth_month_range CHECK (birth_month BETWEEN 1 AND 12),
    customer_id  text        NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    PRIMARY KEY (customer_id, id),
    CONSTRAINT profiles_birth_month_needs_year CHECK (birth_month IS NULL OR birth_year IS NOT NULL)
);

CREATE INDEX profiles_customer_created_at_idx ON profiles (customer_id, created_at, id);

-- Where the customer lives (the service is US-only) and their filing status,
-- which choose the tax tables, plus their planning assumptions. One row per
-- customer.
CREATE TABLE account_profile (
    country         text          NOT NULL DEFAULT 'US'
                                  CONSTRAINT account_profile_country_valid CHECK (country = 'US'),
    postal_code     text          NOT NULL DEFAULT ''
                                  CONSTRAINT account_profile_postal_code_length CHECK (char_length(postal_code) <= 16),
    filing_status   text          NOT NULL DEFAULT 'single'
                                  CONSTRAINT account_profile_filing_status_valid CHECK (filing_status IN ('single', 'marriedJoint')),
    updated_at      timestamptz   NOT NULL DEFAULT now(),
    customer_id     text          PRIMARY KEY REFERENCES customers (id) ON DELETE CASCADE,
    -- Default yearly investment return, expected inflation and the cash rate
    -- (bank accounts and CDs without their own); null until set.
    expected_return double precision
                                  CONSTRAINT account_profile_expected_return_range CHECK (expected_return BETWEEN -0.5 AND 0.5),
    inflation       double precision
                                  CONSTRAINT account_profile_inflation_range CHECK (inflation BETWEEN -0.1 AND 0.2),
    -- Cash to keep on hand (dollars); beyond it, cash is treated as invested.
    emergency_fund  numeric(14,2)
                                  CONSTRAINT account_profile_emergency_fund_nonnegative CHECK (emergency_fund >= 0),
    cash_return     double precision
                                  CONSTRAINT account_profile_cash_return_range CHECK (cash_return >= 0 AND cash_return <= 0.2)
);

-- What-if projections: layers over Main. item_ids is what scenarios saved
-- before layers chose; excluded_item_ids (NULL for those) the Main items a
-- scenario hides. Per-event and per-parameter rules are enforced by the
-- application.
CREATE TABLE scenarios (
    id                text        NOT NULL
                                  CONSTRAINT scenarios_id_format CHECK (id ~ '^[A-Za-z0-9_-]{1,64}$'),
    name              text        NOT NULL
                                  CONSTRAINT scenarios_name_length CHECK (char_length(name) BETWEEN 1 AND 200),
    -- CASE guards jsonb_array_length, which errors on non-arrays.
    item_ids          jsonb       NOT NULL
                                  CONSTRAINT scenarios_item_ids_array CHECK (
                                      CASE WHEN jsonb_typeof(item_ids) = 'array'
                                           THEN jsonb_array_length(item_ids) <= 500
                                           ELSE false END),
    events            jsonb       NOT NULL
                                  CONSTRAINT scenarios_events_array CHECK (
                                      CASE WHEN jsonb_typeof(events) = 'array'
                                           THEN jsonb_array_length(events) <= 100
                                           ELSE false END),
    parameters        jsonb       NOT NULL
                                  CONSTRAINT scenarios_parameters_object CHECK (jsonb_typeof(parameters) = 'object'),
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL DEFAULT now(),
    customer_id       text        NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    excluded_item_ids jsonb       CONSTRAINT scenarios_excluded_item_ids_array CHECK (
                                      excluded_item_ids IS NULL OR (CASE WHEN jsonb_typeof(excluded_item_ids) = 'array'
                                                                         THEN jsonb_array_length(excluded_item_ids) <= 500
                                                                         ELSE false END)),
    PRIMARY KEY (customer_id, id)
);

CREATE INDEX scenarios_customer_created_at_idx ON scenarios (customer_id, created_at, id);

-- Monthly snapshots of net worth (assets, debts) and monthly income and
-- expenses, recorded by the app for the Home graph's history. One per month;
-- the id is the month ("2026-10").
CREATE TABLE snapshots (
    customer_id      text          NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    id               text          NOT NULL
                                   CONSTRAINT snapshots_id_month CHECK (id ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
    year             smallint      NOT NULL CONSTRAINT snapshots_year_range CHECK (year BETWEEN 1900 AND 2150),
    month            smallint      NOT NULL CONSTRAINT snapshots_month_range CHECK (month BETWEEN 1 AND 12),
    net_worth        numeric(16,2) NOT NULL,
    assets           numeric(16,2) NOT NULL CONSTRAINT snapshots_assets_nonnegative CHECK (assets >= 0),
    debts            numeric(16,2) NOT NULL CONSTRAINT snapshots_debts_nonnegative CHECK (debts >= 0),
    monthly_income   numeric(16,2) NOT NULL CONSTRAINT snapshots_income_nonnegative CHECK (monthly_income >= 0),
    monthly_expenses numeric(16,2) NOT NULL CONSTRAINT snapshots_expenses_nonnegative CHECK (monthly_expenses >= 0),
    created_at       timestamptz   NOT NULL,
    updated_at       timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, id),
    CONSTRAINT snapshots_id_matches_month CHECK (id = lpad(year::text, 4, '0') || '-' || lpad(month::text, 2, '0'))
);

-- Records each customer has deleted, per synced collection. Kept so a device
-- holding an old copy can't save it again, and so other devices learn to drop
-- their copies.
CREATE TABLE deletions (
    customer_id text        NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    collection  text        NOT NULL
                            CONSTRAINT deletions_collection_valid CHECK (collection IN ('items', 'profiles', 'scenarios', 'snapshots')),
    id          text        NOT NULL
                            CONSTRAINT deletions_id_format CHECK (id ~ '^[A-Za-z0-9_-]{1,64}$'),
    deleted_at  timestamptz NOT NULL DEFAULT date_trunc('milliseconds', now()),
    PRIMARY KEY (customer_id, collection, id)
);

-- ---------------------------------------------------------------------------
-- US tax parameters by year: reference data shared by every customer (so not
-- keyed by customer_id). The server loads them at startup from the data
-- the taxtables package embeds (taxtables/data), with projected future years,
-- whenever that data changes (tax_data_version); default-data.sql holds the
-- same rows. Nothing returns them to clients.

-- Federal, state and local taxing jurisdictions. `since`: when a local tax
-- began, where known (years before it with no tax_layers row had no tax).
CREATE TABLE tax_jurisdictions (
    id     text PRIMARY KEY
           CONSTRAINT tax_jurisdictions_id_format CHECK (id ~ '^us-(federal|state-[A-Z]{2}|local-[a-z0-9-]+)$'),
    kind   text NOT NULL CONSTRAINT tax_jurisdictions_kind_valid CHECK (kind IN ('federal', 'state', 'local')),
    region text,
    state  char(2) CONSTRAINT tax_jurisdictions_state_format CHECK (state ~ '^[A-Z]{2}$'),
    since  smallint CONSTRAINT tax_jurisdictions_since_range CHECK (since BETWEEN 1900 AND 2200)
);

-- A jurisdiction's income tax in one year. `tax` holds the type's fields
-- (schedules, rate, deduction, percentStandardDeduction, amount, per,
-- minimumEarnings). No row: no such tax that year.
CREATE TABLE tax_layers (
    jurisdiction_id text     NOT NULL REFERENCES tax_jurisdictions (id) ON DELETE CASCADE,
    tax_year        smallint NOT NULL CONSTRAINT tax_layers_year_range CHECK (tax_year BETWEEN 1900 AND 2200),
    type            text     NOT NULL CONSTRAINT tax_layers_type_valid CHECK (type IN ('brackets', 'flat', 'fixed', 'none')),
    tax             jsonb    NOT NULL CONSTRAINT tax_layers_tax_object CHECK (jsonb_typeof(tax) = 'object'),
    source          text     NOT NULL,
    notes           text,
    -- Indexed forward from the last transcribed year rather than published.
    projected       boolean  NOT NULL,
    PRIMARY KEY (jurisdiction_id, tax_year)
);

CREATE INDEX tax_layers_by_year ON tax_layers (tax_year);

-- Employee Social Security and Medicare parameters by year.
CREATE TABLE payroll_tax_years (
    tax_year                     smallint      PRIMARY KEY CONSTRAINT payroll_tax_years_year_range CHECK (tax_year BETWEEN 1900 AND 2200),
    social_security_rate         numeric(7,6)  NOT NULL,
    wage_base                    numeric(12,2) NOT NULL,
    medicare_rate                numeric(7,6)  NOT NULL,
    -- The Medicare wage cap; null from 1994, when it was removed.
    medicare_wage_base           numeric(12,2),
    additional_medicare_rate     numeric(7,6)  NOT NULL,
    additional_medicare_single   numeric(12,2),
    additional_medicare_joint    numeric(12,2),
    source                       text          NOT NULL,
    notes                        text,
    projected                    boolean       NOT NULL
);

-- ZIP codes each state and local jurisdiction covers, as inclusive ranges.
-- 'partial': only part of the ZIP lies inside (the tax applies only if the
-- address does).
CREATE TABLE tax_zip_ranges (
    jurisdiction_id text    NOT NULL REFERENCES tax_jurisdictions (id) ON DELETE CASCADE,
    zip_from        integer NOT NULL,
    zip_to          integer NOT NULL,
    coverage        text    NOT NULL CONSTRAINT tax_zip_ranges_coverage_valid CHECK (coverage IN ('full', 'partial')),
    PRIMARY KEY (jurisdiction_id, zip_from, coverage),
    CONSTRAINT tax_zip_ranges_bounds CHECK (zip_from BETWEEN 0 AND 99999 AND zip_to BETWEEN zip_from AND 99999)
);

-- Which version of the embedded data the tables hold, so startup reloads them
-- only when the data (or the projection) changes.
CREATE TABLE tax_data_version (
    singleton boolean     PRIMARY KEY DEFAULT true CONSTRAINT tax_data_version_singleton CHECK (singleton),
    hash      text        NOT NULL,
    loaded_at timestamptz NOT NULL DEFAULT now()
);

-- The federal treatment of long-term capital gains and qualified dividends by
-- year. `rules` holds the method's fields (excludedShare, maxRate, tiers or
-- schedules, the net investment income tax).
CREATE TABLE capital_gains_tax_years (
    tax_year   smallint PRIMARY KEY CONSTRAINT capital_gains_tax_years_year_range CHECK (tax_year BETWEEN 1900 AND 2200),
    method     text     NOT NULL
                        CONSTRAINT capital_gains_tax_years_method_valid CHECK (method IN ('exclusion', 'maxRate', 'ordinary', 'brackets')),
    rules      jsonb    NOT NULL,
    source     text     NOT NULL,
    notes      text,
    projected  boolean  NOT NULL
);

-- ---------------------------------------------------------------------------
-- This file is the 0032 baseline.
INSERT INTO schema_migrations (version) VALUES ('0032_baseline') ON CONFLICT DO NOTHING;
