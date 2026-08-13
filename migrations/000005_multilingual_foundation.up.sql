DO $$ BEGIN
  CREATE TYPE supported_locale AS ENUM ('tr','en','de','ru');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE users ADD COLUMN preferred_locale supported_locale NOT NULL DEFAULT 'tr';
ALTER TABLE user_profiles ADD COLUMN bio_language supported_locale;
ALTER TABLE posts ADD COLUMN content_language supported_locale;
ALTER TABLE comments ADD COLUMN content_language supported_locale;

ALTER TABLE visitor_sessions ADD COLUMN locale supported_locale;
ALTER TABLE searches ADD COLUMN query_language supported_locale;
ALTER TABLE email_verification_codes ADD COLUMN locale supported_locale NOT NULL DEFAULT 'tr';
ALTER TABLE email_outbox ADD COLUMN locale supported_locale NOT NULL DEFAULT 'tr';
ALTER TABLE push_devices ADD COLUMN locale supported_locale NOT NULL DEFAULT 'tr';
ALTER TABLE notification_outbox
  ADD COLUMN locale supported_locale NOT NULL DEFAULT 'tr',
  ADD COLUMN template_key text,
  ADD COLUMN template_params jsonb NOT NULL DEFAULT '{}';

ALTER TABLE search_query_daily_metrics ADD COLUMN query_language supported_locale NOT NULL DEFAULT 'tr';
ALTER TABLE search_intent_daily_metrics ADD COLUMN query_language supported_locale NOT NULL DEFAULT 'tr';
ALTER TABLE search_intent_daily_metrics DROP CONSTRAINT search_intent_daily_metrics_pkey;
ALTER TABLE search_intent_daily_metrics ADD PRIMARY KEY(metric_date,dimension,value,query_language);

CREATE TABLE store_category_translations (
  category_id uuid NOT NULL REFERENCES store_categories(id) ON DELETE CASCADE,
  locale supported_locale NOT NULL,
  name text NOT NULL,
  description text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(category_id,locale)
);

CREATE TABLE store_translations (
  store_id uuid NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  locale supported_locale NOT NULL,
  display_name text,
  description text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(store_id,locale),
  CHECK(display_name IS NOT NULL OR description IS NOT NULL)
);

CREATE TABLE locale_daily_metrics (
  metric_date date NOT NULL,
  dimension text NOT NULL CHECK(dimension IN ('user_preference','search_query','anonymous_session','email','push_device','notification')),
  locale supported_locale NOT NULL,
  event_count bigint NOT NULL DEFAULT 0,
  zero_result_count bigint NOT NULL DEFAULT 0,
  ai_fallback_count bigint NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(metric_date,dimension,locale)
);

INSERT INTO store_category_translations(category_id,locale,name)
SELECT id,locale::supported_locale,
  CASE slug
    WHEN 'furniture' THEN CASE locale WHEN 'tr' THEN 'Mobilya' WHEN 'en' THEN 'Furniture' WHEN 'de' THEN 'Möbel' ELSE 'Мебель' END
    WHEN 'home_textile' THEN CASE locale WHEN 'tr' THEN 'Ev Tekstili' WHEN 'en' THEN 'Home Textiles' WHEN 'de' THEN 'Heimtextilien' ELSE 'Домашний текстиль' END
    WHEN 'lighting' THEN CASE locale WHEN 'tr' THEN 'Aydınlatma' WHEN 'en' THEN 'Lighting' WHEN 'de' THEN 'Beleuchtung' ELSE 'Освещение' END
    WHEN 'decoration' THEN CASE locale WHEN 'tr' THEN 'Dekorasyon' WHEN 'en' THEN 'Decoration' WHEN 'de' THEN 'Dekoration' ELSE 'Декор' END
    WHEN 'kitchenware' THEN CASE locale WHEN 'tr' THEN 'Mutfak' WHEN 'en' THEN 'Kitchenware' WHEN 'de' THEN 'Küchenbedarf' ELSE 'Кухонные товары' END
    WHEN 'bathroom' THEN CASE locale WHEN 'tr' THEN 'Banyo' WHEN 'en' THEN 'Bathroom' WHEN 'de' THEN 'Badezimmer' ELSE 'Ванная' END
    WHEN 'carpet' THEN CASE locale WHEN 'tr' THEN 'Halı' WHEN 'en' THEN 'Carpets' WHEN 'de' THEN 'Teppiche' ELSE 'Ковры' END
    WHEN 'curtain' THEN CASE locale WHEN 'tr' THEN 'Perde' WHEN 'en' THEN 'Curtains' WHEN 'de' THEN 'Gardinen' ELSE 'Шторы' END
    WHEN 'bedding' THEN CASE locale WHEN 'tr' THEN 'Yatak' WHEN 'en' THEN 'Bedding' WHEN 'de' THEN 'Bettwaren' ELSE 'Постельные принадлежности' END
    WHEN 'tableware' THEN CASE locale WHEN 'tr' THEN 'Sofra' WHEN 'en' THEN 'Tableware' WHEN 'de' THEN 'Geschirr' ELSE 'Посуда' END
    WHEN 'storage' THEN CASE locale WHEN 'tr' THEN 'Depolama' WHEN 'en' THEN 'Storage' WHEN 'de' THEN 'Aufbewahrung' ELSE 'Хранение' END
    WHEN 'home_accessories' THEN CASE locale WHEN 'tr' THEN 'Ev Aksesuarları' WHEN 'en' THEN 'Home Accessories' WHEN 'de' THEN 'Wohnaccessoires' ELSE 'Аксессуары для дома' END
    WHEN 'household' THEN CASE locale WHEN 'tr' THEN 'Ev Gereçleri' WHEN 'en' THEN 'Household' WHEN 'de' THEN 'Haushalt' ELSE 'Товары для дома' END
  END
FROM store_categories CROSS JOIN (VALUES ('tr'),('en'),('de'),('ru')) locales(locale);

CREATE INDEX searches_language_time_idx ON searches(query_language,created_at);
CREATE INDEX users_preferred_locale_idx ON users(preferred_locale) WHERE deleted_at IS NULL;
CREATE INDEX push_devices_locale_idx ON push_devices(locale) WHERE disabled_at IS NULL;
