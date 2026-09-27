// Command moderation-probe asks the review check about sample sentences and prints what it
// found, so a change to the prompt or the model can be looked at before it reaches a review.
//
//	OPENAI_API_KEY=... OPENAI_MODEL=... go run ./cmd/moderation-probe
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/burakaltintas/home-app-api/internal/moderation"
)

// Each sample says what it should produce. Complaints that insult nobody must pass: a check
// that holds honest reviews back teaches people to stop writing them.
var samples = []struct {
	want string
	text string
}{
	{"clean", "Personel ilgisizdi, fiyatlar çok pahalı. Bir daha gitmem."},
	{"clean", "Aradığım nevresimi bulamadım, raflar dağınıktı, berbat bir deneyimdi."},
	{"clean", "Ahmet Bey çok yardımcı oldu, teşekkürler."},
	{"clean", "Kasa çok yavaştı, 20 dakika bekledim. Rezalet."},
	{"insult", "Buradaki eleman şerefsizin teki."},
	{"insult", "Salak salak bakıyorlar, müşteriden anlamıyorlar."},
	{"threat", "O kasiyeri bir daha görürsem ne yapacağımı bilirim, bulurum onu."},
	{"accusation", "Bu mağaza hırsız, sahte ürün satıyorlar."},
	{"personal_data", "Kasiyer Ayşe Yılmaz 0532 123 45 67 çok kaba davrandı."},
}

func main() {
	key, model := os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	checker := moderation.NewOpenAIChecker(key, model, 20*time.Second)
	fmt.Println("model:", model)
	wrong := 0
	for _, s := range samples {
		v, e := checker.Check(context.Background(), moderation.Text(moderation.Parts{Notes: map[string]string{"staff_care": s.text}}))
		got := "clean"
		if e != nil {
			got = "error: " + e.Error()
		} else if v.Severe() {
			got = v.Findings[0].Kind
		}
		mark := "ok "
		if (s.want == "clean") != (got == "clean") {
			mark = "BAD"
			wrong++
		}
		fmt.Printf("%s want %-13s got %-13s %v | %s\n", mark, s.want, got, v.Findings, s.text)
	}
	fmt.Println("wrong:", wrong)
}
