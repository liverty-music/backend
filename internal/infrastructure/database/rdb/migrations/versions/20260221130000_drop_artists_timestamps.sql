-- +goose Up
-- Drop unused timestamp columns from artists table
ALTER TABLE artists DROP COLUMN IF EXISTS created_at;
ALTER TABLE artists DROP COLUMN IF EXISTS updated_at;
