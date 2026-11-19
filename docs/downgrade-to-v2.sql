-- Convert a go-pg-migrate v3 ledger back to the table v2 reads and writes.
--
-- Use it to roll back to an image that still runs go-pg-migrate v2. It works
-- on a ledger that v3 created and on one that v3 upgraded from v2. Every row
-- keeps its version, name, and timestamp. A later v3 run upgrades the table
-- again.
--
-- Preconditions:
--   1. No v3 runner is active, and none starts until the v2 image is the only
--      one running. Stop every service that migrates at boot first.
--   2. No row is dirty. Inspect the database and call Resolve for each dirty
--      version first.
--   3. Every version is between 1 and 2147483647. v2 stores INTEGER numbers.
--   4. Every name has at most 255 characters. v2 stores VARCHAR(255) names.
--
-- The script checks 2 to 4 itself. If one fails, it raises an exception and
-- changes nothing. It also fails, and changes nothing, on a table that is not
-- a v3 ledger.
--
-- Run it as the table owner, in one session:
--
--   psql -v ON_ERROR_STOP=1 -f downgrade-to-v2.sql "$DATABASE_URL"
--
-- The script is one transaction. After a failure, psql without ON_ERROR_STOP
-- skips the remaining statements and its COMMIT rolls back. Any other client
-- must send ROLLBACK, or close the session, after a failure.
--
-- Other ledger locations: the script is written for the default ledger,
-- public.migrations. If Config.Schema or Config.Table is set, replace every
-- "public.migrations" below with your quoted "schema"."table". v2 itself has
-- no such setting: it uses the table named migrations in the first schema on
-- the connection's search_path.
--
-- Time zone: applied_at TIMESTAMPTZ becomes created_at TIMESTAMP in the
-- session time zone, the reverse of the v3 upgrade. Run the script with the
-- TimeZone setting your v2 runners use, so old and new rows agree.
--
-- The result has the v2 columns, defaults, and default object names
-- (migrations_pkey, migrations_number_key, migrations_id_seq). The column
-- order differs from a table v2 created, which v2 does not depend on. v2
-- never reads id; the script numbers it in the order the versions were
-- applied.

BEGIN;

-- Waits for a v3 migration in progress, and blocks new ledger reads.
LOCK TABLE public.migrations IN ACCESS EXCLUSIVE MODE;

DO $downgrade$
DECLARE
    bad bigint;
    old_constraint name;
    found_columns text[];
BEGIN
    -- The exact column set that v3 accepts as its ledger.
    SELECT array_agg(attname::text ORDER BY attname) INTO found_columns
    FROM pg_attribute
    WHERE attrelid = 'public.migrations'::regclass AND attnum > 0 AND NOT attisdropped;
    IF found_columns IS DISTINCT FROM ARRAY['applied_at', 'dirty', 'name', 'version'] THEN
        RAISE EXCEPTION 'downgrade-to-v2: public.migrations is not a v3 ledger, it has columns %', found_columns
            USING HINT = 'A v3 ledger has exactly the columns applied_at, dirty, name, and version.';
    END IF;

    SELECT count(*) INTO bad FROM public.migrations WHERE dirty;
    IF bad > 0 THEN
        RAISE EXCEPTION 'downgrade-to-v2: % dirty row(s) in public.migrations', bad
            USING HINT = 'Inspect the database and call Resolve for each dirty version first.';
    END IF;

    SELECT count(*) INTO bad FROM public.migrations WHERE version NOT BETWEEN 1 AND 2147483647;
    IF bad > 0 THEN
        RAISE EXCEPTION 'downgrade-to-v2: % version(s) outside 1 to 2147483647 in public.migrations', bad
            USING HINT = 'v2 stores INTEGER numbers and cannot record these versions.';
    END IF;

    SELECT count(*) INTO bad FROM public.migrations WHERE char_length(name) > 255;
    IF bad > 0 THEN
        RAISE EXCEPTION 'downgrade-to-v2: % name(s) longer than 255 characters in public.migrations', bad
            USING HINT = 'v2 stores VARCHAR(255) names. Shorten these names first.';
    END IF;

    -- A ledger that v3 created has a primary key on version. One that v3
    -- upgraded from v2 also has the old unique constraint of v2. Drop both,
    -- whatever their names are, so that the v2 names are free again.
    FOR old_constraint IN
        SELECT c.conname
        FROM pg_constraint AS c
        JOIN pg_attribute AS a ON a.attrelid = c.conrelid AND a.attname = 'version'
        WHERE c.conrelid = 'public.migrations'::regclass
            AND c.contype IN ('p', 'u')
            AND c.conkey = ARRAY[a.attnum]
    LOOP
        EXECUTE format('ALTER TABLE public.migrations DROP CONSTRAINT %I', old_constraint);
    END LOOP;
END
$downgrade$;

ALTER TABLE public.migrations RENAME COLUMN version TO number;
ALTER TABLE public.migrations RENAME COLUMN applied_at TO created_at;

ALTER TABLE public.migrations
    DROP COLUMN dirty,
    ALTER COLUMN number TYPE INTEGER,
    ALTER COLUMN number SET NOT NULL,
    ALTER COLUMN name TYPE VARCHAR(255),
    ALTER COLUMN created_at TYPE TIMESTAMP,
    ALTER COLUMN created_at SET DEFAULT NOW();

-- SERIAL creates the sequence migrations_id_seq and fills id in storage order.
ALTER TABLE public.migrations ADD COLUMN id SERIAL;

-- Number id in the order the versions were applied, as v2 did on insert.
UPDATE public.migrations AS m
SET id = o.id
FROM (
    SELECT number, row_number() OVER (ORDER BY created_at, number) AS id
    FROM public.migrations
) AS o
WHERE m.number = o.number;

-- The next v2 insert takes the id after the highest one.
SELECT setval(
    pg_get_serial_sequence('public.migrations', 'id')::regclass,
    coalesce(max(id), 1),
    max(id) IS NOT NULL
)
FROM public.migrations;

-- These get the v2 default names, migrations_pkey and migrations_number_key.
ALTER TABLE public.migrations
    ADD PRIMARY KEY (id),
    ADD UNIQUE (number);

COMMIT;
