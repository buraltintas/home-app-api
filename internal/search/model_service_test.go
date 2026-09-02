package search

import "testing"

// The names are real rows from the catalogue. A repairman answering a search for shops that
// sell white goods is the defect; a shop that is also an authorised repair point for what
// it stocks is not, and it says so in brackets after its own name.
func TestNamesAServiceReadsRepairTrades(t *testing.T) {
	service := []string{
		"Beyaz Eşya Servisi",
		"Aksoy Teknik Servis (Arzum, Fakir, Fantom, Ufo, Stilevs, DeLonghi, Karaca, Braun Yetkili Servis)",
		"Şanlıurfa Beyaz Eşya Tamircisi",
		"SD Teknik Servis - Beyaz Eşya Tamircisi",
		"Servis Plus Beyaz Eşya Klima Kombi Servisi",
		"Hurma beyaz eşya teknik servis",
		"Antalya beyaz eşya ve klima tamir bakım onarım montaj",
	}
	shops := []string{
		"Akay Ev Aletleri, Antalya (Fakir, Stilevs, Arnica, Korkmaz, DeLonghi, Veito, Braun Yetkili Servis)",
		"Semih Ticaret - Teknik İş (Tefal,Rowenta,King Yetkili Servisi)",
		"Yörük Ticaret Antalya (KARACA ARNİCA FAKİR KORKMAZ DELONGHI BRAUN FERRE NİLFİSK YETKİLİ SERVİS)",
		"YIKILMAZ MOBİLYA montaj mutfak Yüklük Vestiger Banyo dolabı Parke Kapı Tüm mobilya imalati ve tamir montaj kırım kesme isleri",
		"Şıhın Yeri Beyaz Eşya Mağazası",
		"Karataş Beyaz Eşya Ve Mobilya Dünyası",
	}
	for _, name := range service {
		normalized := normalizeText(name)
		if !namesAService(normalized, foldLatin(normalized)) {
			t.Errorf("%q should read as a service", name)
		}
	}
	for _, name := range shops {
		normalized := normalizeText(name)
		if namesAService(normalized, foldLatin(normalized)) {
			t.Errorf("%q is a shop, not a service", name)
		}
	}
}
