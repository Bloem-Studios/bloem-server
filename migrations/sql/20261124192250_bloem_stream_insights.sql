-- +goose Up
-- +goose StatementBegin
-- Stream insights for the Bloem admin apps (docs/architecture/bloem-stream-insights.md).
--
-- Bloem-only. One row per playback session, written from the playback
-- lifecycle events, and one per refusal at a stream or transcode limit. No
-- viewer IP address is stored: the viewer columns hold the location the
-- address resolved to. Nothing here references or constrains a table Silo
-- writes. Rows are kept 90 days.
CREATE TABLE public.bloem_stream_records (
    session_id            text PRIMARY KEY,
    user_id               integer NOT NULL,
    profile_id            text NOT NULL DEFAULT '',
    started_at            timestamptz NOT NULL,
    ended_at              timestamptz,
    updated_at            timestamptz NOT NULL DEFAULT now(),
    last_seq              bigint NOT NULL DEFAULT 0,
    client_name           text NOT NULL DEFAULT '',
    client_version        text NOT NULL DEFAULT '',
    device_platform       text NOT NULL DEFAULT '',
    play_method           text NOT NULL DEFAULT '',
    effective_play_method text NOT NULL DEFAULT '',
    video_decision        text NOT NULL DEFAULT '',
    audio_decision        text NOT NULL DEFAULT '',
    adaptation_reasons    text[] NOT NULL DEFAULT '{}',
    node_id               integer,
    node_name             text NOT NULL DEFAULT '',
    viewer_city           text NOT NULL DEFAULT '',
    viewer_country        text NOT NULL DEFAULT '',
    viewer_country_code   text NOT NULL DEFAULT '',
    viewer_lat            double precision,
    viewer_lon            double precision,
    viewer_approx         boolean NOT NULL DEFAULT false,
    cf_colo               text NOT NULL DEFAULT '',
    edge_name             text NOT NULL DEFAULT '',
    edge_lat              double precision,
    edge_lon              double precision,
    viewer_transport      text NOT NULL DEFAULT 'unknown'
        CHECK (viewer_transport IN ('udp', 'tcp', 'cloudflare', 'unknown')),
    viewer_proto          text NOT NULL DEFAULT '',
    server_proto          text NOT NULL DEFAULT '',
    transport_changed     boolean NOT NULL DEFAULT false
);
CREATE INDEX bloem_stream_records_ended_at_idx ON public.bloem_stream_records (ended_at);
CREATE INDEX bloem_stream_records_started_at_idx ON public.bloem_stream_records (started_at);
CREATE INDEX bloem_stream_records_user_started_idx ON public.bloem_stream_records (user_id, started_at);

CREATE TABLE public.bloem_limit_refusals (
    id         bigserial PRIMARY KEY,
    user_id    integer NOT NULL,
    profile_id text NOT NULL DEFAULT '',
    kind       text NOT NULL CHECK (kind IN ('streams', 'transcodes')),
    scope      text NOT NULL CHECK (scope IN ('person', 'household', 'organization')),
    refused_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bloem_limit_refusals_refused_at_idx ON public.bloem_limit_refusals (refused_at);
CREATE INDEX bloem_limit_refusals_user_refused_idx ON public.bloem_limit_refusals (user_id, refused_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.bloem_limit_refusals;
DROP TABLE IF EXISTS public.bloem_stream_records;
-- +goose StatementEnd
