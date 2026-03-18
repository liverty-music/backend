-- Create ticket journeys table for user-managed ticket acquisition tracking
CREATE TABLE ticket_journeys (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    status SMALLINT NOT NULL,
    PRIMARY KEY (user_id, event_id),
    CONSTRAINT chk_ticket_journeys_status CHECK (status BETWEEN 1 AND 5)
);

COMMENT ON TABLE ticket_journeys IS 'Per-user ticket acquisition status tracking for events. Status values: 1=TRACKING, 2=APPLIED, 3=LOST, 4=UNPAID, 5=PAID';
COMMENT ON COLUMN ticket_journeys.user_id IS 'Reference to the fan tracking this event';
COMMENT ON COLUMN ticket_journeys.event_id IS 'Reference to the event being tracked';
COMMENT ON COLUMN ticket_journeys.status IS 'Ticket journey status: 1=TRACKING, 2=APPLIED, 3=LOST, 4=UNPAID, 5=PAID';

CREATE INDEX idx_ticket_journeys_event_id ON ticket_journeys(event_id);
