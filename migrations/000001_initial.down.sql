DROP TABLE IF EXISTS notification_outbox, notification_preferences, push_devices, email_deliveries, email_outbox,
 search_interactions, search_results, searches, follows, comments, likes, favorites, post_media, posts, media,
 store_stats, store_external_sources, store_category_links, stores, store_categories, email_verification_codes,
 visitor_sessions, auth_sessions, auth_identities, user_private_profiles, user_profiles, users CASCADE;
DROP EXTENSION IF EXISTS postgis;
DROP EXTENSION IF EXISTS citext;
DROP EXTENSION IF EXISTS pgcrypto;
