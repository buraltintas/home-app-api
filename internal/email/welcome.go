package email

import (
	"bytes"
	"html/template"

	"github.com/burakaltintas/home-app-api/internal/brand"
	"github.com/burakaltintas/home-app-api/internal/i18n"
)

// The welcome mail is the first thing a new account ever receives. It carries no code,
// no token and no link that grants access, so it is safe to retry and safe to render
// from a payload that holds nothing but the locale the person signed up in.

type welcomeStep struct{ Title, Body string }

type welcomeCopy struct {
	Subject, Preheader, Eyebrow, Title, Intro string
	Steps                                     []welcomeStep
	CTALabel, Closing, Footer                 string
}

type welcomeTemplateData struct {
	Locale, Brand, URL string
	Copy               welcomeCopy
}

// RenderWelcome builds the welcome message in the locale the account was created in,
// falling back to the default locale rather than failing, because a new account is worth
// an email in the wrong language more than it is worth no email at all.
func RenderWelcome(locale i18n.Locale) (Message, error) {
	copy, ok := welcomeTemplates[locale]
	if !ok {
		locale = i18n.DefaultLocale
		copy = welcomeTemplates[locale]
	}
	data := welcomeTemplateData{Locale: string(locale), Brand: brand.ProductName, URL: brand.WebsiteURL, Copy: copy}
	var html, text bytes.Buffer
	if e := welcomeHTMLTemplate.Execute(&html, data); e != nil {
		return Message{}, e
	}
	if e := welcomeTextTemplate.Execute(&text, data); e != nil {
		return Message{}, e
	}
	return Message{Subject: copy.Subject, HTML: html.String(), Text: text.String()}, nil
}

var welcomeTemplates = map[i18n.Locale]welcomeCopy{
	i18n.LocaleTR: {
		Subject:   brand.ProductName + " ailesine hoş geldiniz",
		Preheader: "Gerçekten gidilmeye değer mağazaları keşfetmeye başlayın.",
		Eyebrow:   "HOŞ GELDİNİZ",
		Title:     "Aramıza hoş geldiniz",
		Intro:     brand.ProductName + " ile eviniz ve yaşam alanınız için gerçekten gidilmeye değer fiziksel mağazaları bulursunuz. Başlamak için yapabilecekleriniz:",
		Steps: []welcomeStep{
			{Title: "Mağaza keşfedin", Body: "Aradığınız ürünü ya da kategoriyi yazın, çevrenizdeki gerçek mağazaları görün."},
			{Title: "Ziyaretinizi doğrulayın", Body: "Mağazanın yakınındayken konumunuzu paylaşın; böylece yorumunuz gerçek bir ziyarete dayanır."},
			{Title: "Deneyiminizi yazın", Body: "Puan verin, fotoğraf ekleyin ve orada ne bulduğunuzu anlatın."},
			{Title: "Favorilerinizi biriktirin", Body: "Beğendiğiniz mağazaları kaydedin, sonra kaldığınız yerden devam edin."},
		},
		CTALabel: "Keşfetmeye başlayın",
		Closing:  "Bu e-postayı, bu adresle bir " + brand.ProductName + " hesabı oluşturulduğu için aldınız.",
		Footer:   "Gerçek mağazaları, gerçek ziyaretlerle keşfedin.",
	},
	i18n.LocaleEN: {
		Subject:   "Welcome to " + brand.ProductName,
		Preheader: "Start discovering physical stores that are actually worth the trip.",
		Eyebrow:   "WELCOME",
		Title:     "Welcome aboard",
		Intro:     brand.ProductName + " helps you find real, physical home and living stores that are worth visiting. Here is what you can do to get started:",
		Steps: []welcomeStep{
			{Title: "Discover stores", Body: "Type the product or category you are after and see the real stores around you."},
			{Title: "Verify your visit", Body: "Share your location while you are near the store so your review is backed by a real visit."},
			{Title: "Write what you found", Body: "Leave a rating, add photos and tell people what the place is really like."},
			{Title: "Build your favourites", Body: "Save the stores you liked and pick up where you left off."},
		},
		CTALabel: "Start exploring",
		Closing:  "You are receiving this email because a " + brand.ProductName + " account was created with this address.",
		Footer:   "Discover real stores through real visits.",
	},
	i18n.LocaleDE: {
		Subject:   "Willkommen bei " + brand.ProductName,
		Preheader: "Entdecken Sie Geschäfte, für die sich der Weg wirklich lohnt.",
		Eyebrow:   "WILLKOMMEN",
		Title:     "Willkommen an Bord",
		Intro:     "Mit " + brand.ProductName + " finden Sie echte Geschäfte für Wohnen und Einrichten, deren Besuch sich lohnt. So legen Sie los:",
		Steps: []welcomeStep{
			{Title: "Geschäfte entdecken", Body: "Geben Sie das gesuchte Produkt oder die Kategorie ein und sehen Sie die echten Geschäfte in Ihrer Nähe."},
			{Title: "Besuch bestätigen", Body: "Teilen Sie Ihren Standort, während Sie in der Nähe sind, damit Ihre Bewertung auf einem echten Besuch beruht."},
			{Title: "Erfahrung teilen", Body: "Vergeben Sie eine Bewertung, fügen Sie Fotos hinzu und beschreiben Sie, was Sie vorgefunden haben."},
			{Title: "Favoriten sammeln", Body: "Speichern Sie Geschäfte, die Ihnen gefallen, und machen Sie später dort weiter."},
		},
		CTALabel: "Jetzt entdecken",
		Closing:  "Sie erhalten diese E-Mail, weil mit dieser Adresse ein " + brand.ProductName + " Konto erstellt wurde.",
		Footer:   "Entdecken Sie echte Geschäfte durch echte Besuche.",
	},
	i18n.LocaleRU: {
		Subject:   "Добро пожаловать в " + brand.ProductName,
		Preheader: "Начните находить магазины, ради которых стоит выйти из дома.",
		Eyebrow:   "ДОБРО ПОЖАЛОВАТЬ",
		Title:     "Рады видеть вас",
		Intro:     brand.ProductName + " помогает находить настоящие магазины товаров для дома, которые стоит посетить. С чего начать:",
		Steps: []welcomeStep{
			{Title: "Находите магазины", Body: "Введите товар или категорию и посмотрите реальные магазины рядом с вами."},
			{Title: "Подтверждайте визит", Body: "Поделитесь геопозицией рядом с магазином, чтобы отзыв опирался на настоящее посещение."},
			{Title: "Делитесь впечатлениями", Body: "Поставьте оценку, добавьте фотографии и расскажите, что вы там увидели."},
			{Title: "Собирайте избранное", Body: "Сохраняйте понравившиеся магазины и возвращайтесь к ним позже."},
		},
		CTALabel: "Начать поиск",
		Closing:  "Вы получили это письмо, потому что с этим адресом был создан аккаунт " + brand.ProductName + ".",
		Footer:   "Открывайте настоящие магазины благодаря реальным посещениям.",
	},
}

var welcomeHTMLTemplate = template.Must(template.New("welcome-html").Parse(`<!doctype html>
<html lang="{{.Locale}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light dark">
  <meta name="supported-color-schemes" content="light dark">
  <title>{{.Copy.Subject}}</title>
  <style>
    :root { color-scheme: light dark; supported-color-schemes: light dark; }
    body, table, td, p, h1, h2 { margin: 0; padding: 0; }
    table { border-collapse: collapse; border-spacing: 0; }
    img { border: 0; display: block; }
    .email-canvas { background-color: #F7F5F0 !important; }
    .email-surface { background-color: #FFFEFB !important; border-color: #D9D5CC !important; }
    .email-ink { color: #262521 !important; }
    .email-muted { color: #706D65 !important; }
    .email-line { border-color: #D9D5CC !important; }
    .email-accent { color: #A34A32 !important; }
    .email-step { background-color: #EFECE5 !important; }
    @media (prefers-color-scheme: dark) {
      .email-canvas { background-color: #171714 !important; }
      .email-surface { background-color: #24231F !important; border-color: #48463F !important; }
      .email-ink { color: #F7F5F0 !important; }
      .email-muted { color: #C5C0B6 !important; }
      .email-line { border-color: #48463F !important; }
      .email-accent { color: #F0A58F !important; }
      .email-step { background-color: #302521 !important; }
    }
    [data-ogsc] .email-canvas { background-color: #171714 !important; }
    [data-ogsc] .email-surface { background-color: #24231F !important; border-color: #48463F !important; }
    [data-ogsc] .email-ink { color: #F7F5F0 !important; }
    [data-ogsc] .email-muted { color: #C5C0B6 !important; }
    [data-ogsc] .email-line { border-color: #48463F !important; }
    [data-ogsc] .email-accent { color: #F0A58F !important; }
    [data-ogsc] .email-step { background-color: #302521 !important; }
    @media only screen and (max-width: 620px) {
      .email-shell { width: 100% !important; }
      .email-gutter { padding-left: 20px !important; padding-right: 20px !important; }
      .email-card { padding: 32px 24px !important; }
      .email-title { font-size: 27px !important; line-height: 34px !important; }
    }
  </style>
</head>
<body class="email-canvas" style="margin:0; padding:0; width:100%; background-color:#F7F5F0; -webkit-text-size-adjust:100%; -ms-text-size-adjust:100%;">
  <div style="display:none; max-height:0; overflow:hidden; opacity:0; color:transparent; mso-hide:all;">{{.Copy.Preheader}}</div>
  <table role="presentation" width="100%" class="email-canvas" bgcolor="#F7F5F0" style="width:100%; background-color:#F7F5F0;">
    <tr>
      <td align="center" class="email-gutter" style="padding:40px 24px;">
        <table role="presentation" width="600" class="email-shell" style="width:600px; max-width:600px;">
          <tr>
            <td style="padding:0 0 20px 0;">
              <table role="presentation">
                <tr>
                  <td width="4" bgcolor="#A34A32" style="width:4px; background-color:#A34A32; font-size:0; line-height:0;">&nbsp;</td>
                  <td class="email-ink" style="padding-left:12px; color:#262521; font-family:Arial,'Helvetica Neue',sans-serif; font-size:20px; line-height:26px; font-weight:700; letter-spacing:-0.2px;">{{.Brand}}</td>
                </tr>
              </table>
            </td>
          </tr>
          <tr>
            <td class="email-surface email-card" bgcolor="#FFFEFB" style="padding:48px 48px 44px 48px; background-color:#FFFEFB; border:1px solid #D9D5CC;">
              <p class="email-accent" style="margin:0 0 12px 0; color:#A34A32; font-family:Arial,'Helvetica Neue',sans-serif; font-size:12px; line-height:16px; font-weight:700; letter-spacing:1.4px;">{{.Copy.Eyebrow}}</p>
              <h1 class="email-ink email-title" style="margin:0; color:#262521; font-family:Georgia,'Times New Roman',serif; font-size:32px; line-height:39px; font-weight:700; letter-spacing:-0.4px;">{{.Copy.Title}}</h1>
              <p class="email-muted" style="margin:16px 0 0 0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:16px; line-height:25px;">{{.Copy.Intro}}</p>
              <table role="presentation" width="100%" style="width:100%; margin-top:28px;">
                {{range .Copy.Steps}}<tr>
                  <td class="email-step" bgcolor="#EFECE5" style="padding:16px 18px; background-color:#EFECE5; border-radius:6px;">
                    <h2 class="email-ink" style="margin:0; color:#262521; font-family:Arial,'Helvetica Neue',sans-serif; font-size:15px; line-height:22px; font-weight:700;">{{.Title}}</h2>
                    <p class="email-muted" style="margin:4px 0 0 0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:14px; line-height:22px;">{{.Body}}</p>
                  </td>
                </tr>
                <tr><td style="font-size:0; line-height:0; height:10px;">&nbsp;</td></tr>{{end}}
              </table>
              <table role="presentation" style="margin-top:14px;">
                <tr>
                  <td bgcolor="#A34A32" style="background-color:#A34A32; border-radius:6px;">
                    <a href="{{.URL}}" style="display:inline-block; padding:14px 26px; color:#FFFEFB; font-family:Arial,'Helvetica Neue',sans-serif; font-size:15px; line-height:20px; font-weight:700; text-decoration:none;">{{.Copy.CTALabel}}</a>
                  </td>
                </tr>
              </table>
              <table role="presentation" width="100%" style="width:100%; margin-top:28px;">
                <tr><td class="email-line" style="border-top:1px solid #D9D5CC; font-size:0; line-height:0;">&nbsp;</td></tr>
              </table>
              <p class="email-muted" style="margin:20px 0 0 0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:13px; line-height:20px;">{{.Copy.Closing}}</p>
            </td>
          </tr>
          <tr>
            <td align="center" style="padding:22px 20px 0 20px;">
              <p class="email-muted" style="margin:0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:12px; line-height:18px;">{{.Copy.Footer}}</p>
              <p class="email-muted" style="margin:4px 0 0 0; color:#706D65; font-family:Arial,'Helvetica Neue',sans-serif; font-size:12px; line-height:18px;">bosagezme.com</p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`))

var welcomeTextTemplate = template.Must(template.New("welcome-text").Parse(`{{.Brand}}

{{.Copy.Title}}
{{.Copy.Intro}}
{{range .Copy.Steps}}
- {{.Title}}: {{.Body}}
{{- end}}

{{.Copy.CTALabel}}: {{.URL}}

{{.Copy.Closing}}

{{.Copy.Footer}}
bosagezme.com`))
