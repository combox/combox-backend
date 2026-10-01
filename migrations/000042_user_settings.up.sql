-- Global app settings behind the "Notifications and Sounds" and
-- "Data and Storage" sections. One row per (user, key); a MISSING row means
-- "the user never touched this key" and the service falls back to the
-- documented default, so defaults are NOT materialised here.
--
-- The authoritative whitelist and defaults live in
-- internal/service/settings:
--   notifications_enabled    -> true    notification_previews      -> true
--   sounds_enabled           -> true    badge_enabled             -> true
--   voice_autoplay           -> false   media_autoplay            -> true
--   data_saver               -> false   auto_download_photos      -> true
--   auto_download_videos     -> false   auto_download_files       -> false
--
-- value: exactly 'true' or 'false', enforced by the service layer.
--
-- NOTE: this is the per-user app settings table, unrelated to the valkey
-- profile:settings:{userID} hash behind GET /profile/settings.
CREATE TABLE IF NOT EXISTS user_settings (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
