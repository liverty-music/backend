-- Baseline of the reference data directory. The backend-reference-data
-- AtlasMigration starts from this version (spec.baseline): the schema
-- migrations have already created the tables the later files fill, so the
-- database is not empty when this directory is first applied.
SELECT 1;
