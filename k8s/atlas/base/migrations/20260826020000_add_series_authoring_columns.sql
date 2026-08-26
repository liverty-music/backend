-- Add first-party organizer authoring to the "series" aggregate
-- (organizer-event-authoring change): description, cover image, owning
-- organizer, visibility, publish lifecycle, backend-only unlisted share token,
-- and lifecycle timestamps. All columns are nullable and a CHECK ties the
-- first-party trio together, so existing discovery-pipeline series (organizer_id
-- NULL) are unaffected.

-- First-party visibility enum. PASSWORD is reserved for a future authoring
-- extension; add it with ALTER TYPE ... ADD VALUE when introduced.
CREATE TYPE series_visibility AS ENUM ('PUBLIC', 'UNLISTED');
COMMENT ON TYPE series_visibility IS 'Who can reach a first-party (organizer-authored) series: PUBLIC (normal discovery) or UNLISTED (signed tokenized URL only). NULL for discovered series.';

-- First-party authoring lifecycle enum. SCHEDULED is reserved for a future
-- authoring extension; add it with ALTER TYPE ... ADD VALUE when introduced.
CREATE TYPE series_publish_state AS ENUM ('DRAFT', 'PUBLISHED', 'CANCELLED');
COMMENT ON TYPE series_publish_state IS 'Authoring lifecycle of a first-party series: DRAFT (console-only), PUBLISHED (live), CANCELLED (terminal). NULL for discovered series.';

ALTER TABLE series
    ADD COLUMN description TEXT,
    ADD COLUMN cover_image_url TEXT,
    ADD COLUMN organizer_id UUID REFERENCES organizers(id),
    ADD COLUMN visibility series_visibility,
    ADD COLUMN publish_state series_publish_state,
    ADD COLUMN unlisted_token TEXT,
    ADD COLUMN published_at TIMESTAMPTZ,
    ADD COLUMN cancelled_at TIMESTAMPTZ,
    ADD CONSTRAINT chk_series_first_party_state CHECK (
        (organizer_id IS NULL AND visibility IS NULL AND publish_state IS NULL)
        OR (organizer_id IS NOT NULL AND visibility IS NOT NULL AND publish_state IS NOT NULL)
    );

COMMENT ON COLUMN series.description IS 'Free-form body text of a first-party series page, authored by an organizer. NULL for discovered series or organizer series without a write-up.';
COMMENT ON COLUMN series.cover_image_url IS 'Served URL of the organizer-uploaded cover image (object storage). NULL when no image was uploaded.';
COMMENT ON COLUMN series.organizer_id IS 'Owning organizer for a first-party series; NULL marks a discovery-pipeline series. Non-null makes the series organizer-authored.';
COMMENT ON COLUMN series.visibility IS 'First-party visibility (PUBLIC / UNLISTED). NULL for discovered series.';
COMMENT ON COLUMN series.publish_state IS 'First-party authoring lifecycle (DRAFT / PUBLISHED / CANCELLED). NULL for discovered series.';
COMMENT ON COLUMN series.unlisted_token IS 'Backend-only HMAC-derived share token for an UNLISTED series; rotated by RegenerateToken. NULL unless the series is UNLISTED. Never exposed on read DTOs.';
COMMENT ON COLUMN series.published_at IS 'When the series was first published (first PUBLISHED transition). NULL while DRAFT.';
COMMENT ON COLUMN series.cancelled_at IS 'When the series was cancelled. NULL unless CANCELLED.';

CREATE INDEX idx_series_organizer_id ON series(organizer_id) WHERE organizer_id IS NOT NULL;
COMMENT ON INDEX idx_series_organizer_id IS 'Optimizes listing an organizer own authored series (List) and ownership checks';

CREATE INDEX idx_series_first_party_state ON series(publish_state, visibility) WHERE organizer_id IS NOT NULL;
COMMENT ON INDEX idx_series_first_party_state IS 'Optimizes publish-state / visibility filtering for the shared guard that excludes DRAFT / UNLISTED / CANCELLED series from public and follower surfaces';

CREATE UNIQUE INDEX uq_series_unlisted_token ON series(unlisted_token) WHERE unlisted_token IS NOT NULL;
COMMENT ON INDEX uq_series_unlisted_token IS 'Resolves an UNLISTED series from its share token on the public read path and enforces token uniqueness';
