-- +goose Up
CREATE TABLE bloem_managed_recommendation_feedback (
 installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 profile_id text NOT NULL,
 content_id text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 dismissed boolean NOT NULL,
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(installation_id,user_id,profile_id,content_id)
);
CREATE INDEX bloem_managed_recommendation_feedback_profile ON bloem_managed_recommendation_feedback(user_id,profile_id,content_id) WHERE dismissed;
-- +goose Down
DROP TABLE bloem_managed_recommendation_feedback;
