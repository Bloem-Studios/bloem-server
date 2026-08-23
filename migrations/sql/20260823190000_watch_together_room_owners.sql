-- +goose Up
CREATE SEQUENCE watch_together_room_owner_generation_seq;

CREATE TABLE watch_together_room_owners (
    room_id text PRIMARY KEY REFERENCES watch_together_rooms(id) ON DELETE CASCADE,
    node_id text NOT NULL,
    generation bigint NOT NULL DEFAULT nextval('watch_together_room_owner_generation_seq') CHECK (generation > 0),
    lease_until timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER SEQUENCE watch_together_room_owner_generation_seq
    OWNED BY watch_together_room_owners.generation;

CREATE INDEX watch_together_room_owners_lease_idx
    ON watch_together_room_owners (lease_until);

-- +goose Down
DROP TABLE watch_together_room_owners;
