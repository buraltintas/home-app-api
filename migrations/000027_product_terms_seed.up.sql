-- The words people search with, mapped to the categories this catalogue holds.
--
-- The deterministic parser already knows the obvious ones -- "perde", "yatak", "halı" --
-- because they are category names. These are the rest: the words for the thing rather than
-- for the department. Nobody searches for "furniture", they search for a gardırop.
--
-- A table rather than a constant, because this list is never finished: an administrator
-- adds the word somebody searched for and found nothing, and it works from the next
-- request. That is also why every row carries where it came from.
INSERT INTO product_terms(term,locale,category_slug,source) VALUES
  ('gardırop','tr','furniture','seed'),('gardrop','tr','furniture','seed'),
  ('şifonyer','tr','furniture','seed'),('komodin','tr','furniture','seed'),
  ('kanepe','tr','furniture','seed'),('koltuk','tr','furniture','seed'),
  ('berjer','tr','furniture','seed'),('puf','tr','furniture','seed'),
  ('sehpa','tr','furniture','seed'),('zigon','tr','furniture','seed'),
  ('konsol','tr','furniture','seed'),('vitrin','tr','furniture','seed'),
  ('vestiyer','tr','furniture','seed'),('kitaplık','tr','furniture','seed'),
  ('portmanto','tr','furniture','seed'),('şezlong','tr','garden','seed'),
  ('yatak odası','tr','furniture','seed'),('çocuk odası','tr','furniture','seed'),
  ('genç odası','tr','furniture','seed'),('yemek odası','tr','furniture','seed'),
  ('baza','tr','bedding','seed'),('şilte','tr','bedding','seed'),
  ('nevresim','tr','home_textile','seed'),('yorgan','tr','home_textile','seed'),
  ('yastık','tr','home_textile','seed'),('pike','tr','home_textile','seed'),
  ('battaniye','tr','home_textile','seed'),('kırlent','tr','home_textile','seed'),
  ('havlu','tr','home_textile','seed'),('bornoz','tr','home_textile','seed'),
  ('masa örtüsü','tr','home_textile','seed'),('çeyiz','tr','home_textile','seed'),
  ('tül','tr','curtain','seed'),('stor','tr','curtain','seed'),
  ('zebra perde','tr','curtain','seed'),('jaluzi','tr','curtain','seed'),
  ('fon perde','tr','curtain','seed'),('korniş','tr','curtain','seed'),
  ('kilim','tr','carpet','seed'),('seccade','tr','carpet','seed'),
  ('paspas','tr','carpet','seed'),('halıfleks','tr','carpet','seed'),
  ('yolluk','tr','carpet','seed'),
  ('avize','tr','lighting','seed'),('aplik','tr','lighting','seed'),
  ('abajur','tr','lighting','seed'),('sarkıt','tr','lighting','seed'),
  ('lambader','tr','lighting','seed'),('spot','tr','lighting','seed'),
  ('tencere','tr','kitchenware','seed'),('tava','tr','kitchenware','seed'),
  ('çaydanlık','tr','kitchenware','seed'),('semaver','tr','kitchenware','seed'),
  ('düdüklü','tr','kitchenware','seed'),('kesme tahtası','tr','kitchenware','seed'),
  ('bıçak seti','tr','kitchenware','seed'),
  ('yemek takımı','tr','tableware','seed'),('kahvaltı takımı','tr','tableware','seed'),
  ('çay seti','tr','tableware','seed'),('bardak','tr','tableware','seed'),
  ('tabak','tr','tableware','seed'),('kupa','tr','tableware','seed'),
  ('duşakabin','tr','bathroom','seed'),('klozet','tr','bathroom','seed'),
  ('lavabo','tr','bathroom','seed'),('banyo dolabı','tr','bathroom','seed'),
  ('armatür','tr','bathroom','seed'),
  ('vazo','tr','decoration','seed'),('tablo','tr','decoration','seed'),
  ('mumluk','tr','decoration','seed'),('ayna','tr','decoration','seed'),
  ('saksı','tr','decoration','seed'),('biblo','tr','decoration','seed'),
  ('buzdolabı','tr','major_appliances','seed'),('çamaşır makinesi','tr','major_appliances','seed'),
  ('bulaşık makinesi','tr','major_appliances','seed'),('fırın','tr','major_appliances','seed'),
  ('süpürge','tr','small_appliances','seed'),('blender','tr','small_appliances','seed'),
  ('kettle','tr','small_appliances','seed'),('tost makinesi','tr','small_appliances','seed'),
  ('ütü','tr','small_appliances','seed'),
  ('askılık','tr','household','seed'),('çöp kovası','tr','household','seed'),
  ('saklama kabı','tr','household','seed'),('kurutmalık','tr','household','seed')
ON CONFLICT DO NOTHING;
