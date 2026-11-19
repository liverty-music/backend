-- remove-concert-suppression-and-fix-removal-leftovers (design D3, task 5.2):
-- deleting a Concert no longer records its slot and discovery no longer reads
-- one (backend v1.133.0), so the suppression records are dropped. Before the
-- drop prod held 26 rows, none at a Venue and date of a published Event.

SET search_path TO app, public;

DROP TABLE suppressed_concerts;
