-- +goose NO TRANSACTION
-- +goose Up
-- Match the profile equality prefix and both v2 keyset ordering columns.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_user_ratings_page_order
ON user_ratings (user_id, profile_id, rated_at DESC, media_item_id DESC);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_user_ratings_page_order;
