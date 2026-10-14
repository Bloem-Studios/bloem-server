-- +goose Up
CREATE TABLE public.bloem_preview_event_counts (
    day date NOT NULL,
    event text NOT NULL CHECK (event IN ('cta_shown','cta_clicked','session_ended')),
    count bigint NOT NULL DEFAULT 0 CHECK (count>=0),
    PRIMARY KEY (day,event)
);
COMMENT ON TABLE public.bloem_preview_event_counts IS 'UTC daily aggregate preview counters. No identity, address, browser data, or event payload.';

-- +goose Down
DROP TABLE public.bloem_preview_event_counts;
