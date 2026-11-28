-- Backfill again: the release before known names kept registering artists until the rollout finished, and its
-- inserts took the column default, so those artists are found by no name (design, Migration Plan step 3 of the
-- OpenSpec change match-similar-artists-by-name). Names are normalized as in 20261128133546_add_artist_known_names.sql.
UPDATE "artists" SET "known_names" = ARRAY[lower(btrim(regexp_replace(normalize("name", NFKC), '\s+', ' ', 'g')))] WHERE "known_names" = '{}';
