package notification

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

const (
	TemplateFollowReceived        = "FOLLOW_RECEIVED"
	TemplatePostLiked             = "POST_LIKED"
	TemplatePostCommented         = "POST_COMMENTED"
	TemplateFavoriteStoreActivity = "FAVORITE_STORE_ACTIVITY"
)

type localizedTemplate struct {
	Title string
	Body  string
}

var templates = map[i18n.Locale]map[string]localizedTemplate{
	i18n.LocaleTR: {
		TemplateFollowReceived:        {"Yeni takipçi", "{{.actor}} sizi takip etmeye başladı."},
		TemplatePostLiked:             {"Yeni beğeni", "{{.actor}} gönderinizi beğendi."},
		TemplatePostCommented:         {"Yeni yorum", "{{.actor}} gönderinize yorum yaptı."},
		TemplateFavoriteStoreActivity: {"Favori mağazanızda yenilik", "{{.store}} mağazasında yeni bir etkinlik var."},
	},
	i18n.LocaleEN: {
		TemplateFollowReceived:        {"New follower", "{{.actor}} started following you."},
		TemplatePostLiked:             {"New like", "{{.actor}} liked your post."},
		TemplatePostCommented:         {"New comment", "{{.actor}} commented on your post."},
		TemplateFavoriteStoreActivity: {"Activity at a favorite store", "There is new activity at {{.store}}."},
	},
	i18n.LocaleDE: {
		TemplateFollowReceived:        {"Neue Followerin oder neuer Follower", "{{.actor}} folgt Ihnen jetzt."},
		TemplatePostLiked:             {"Neue Gefällt-mir-Angabe", "{{.actor}} gefällt Ihr Beitrag."},
		TemplatePostCommented:         {"Neuer Kommentar", "{{.actor}} hat Ihren Beitrag kommentiert."},
		TemplateFavoriteStoreActivity: {"Neuigkeiten in einem favorisierten Geschäft", "Bei {{.store}} gibt es neue Aktivitäten."},
	},
	i18n.LocaleRU: {
		TemplateFollowReceived:        {"Новый подписчик", "{{.actor}} подписался на вас."},
		TemplatePostLiked:             {"Новая отметка «Нравится»", "{{.actor}} отметил вашу публикацию."},
		TemplatePostCommented:         {"Новый комментарий", "{{.actor}} прокомментировал вашу публикацию."},
		TemplateFavoriteStoreActivity: {"Новости избранного магазина", "В магазине {{.store}} появилась новая активность."},
	},
}

func RenderTemplate(locale i18n.Locale, key string, params map[string]any) (string, string, error) {
	localized, ok := templates[locale][key]
	if !ok {
		localized, ok = templates[i18n.DefaultLocale][key]
	}
	if !ok {
		return "", "", fmt.Errorf("unknown notification template %q", key)
	}
	render := func(name, source string) (string, error) {
		parsed, err := template.New(name).Option("missingkey=error").Parse(source)
		if err != nil {
			return "", err
		}
		var output bytes.Buffer
		if err = parsed.Execute(&output, params); err != nil {
			return "", err
		}
		return output.String(), nil
	}
	title, err := render("title", localized.Title)
	if err != nil {
		return "", "", err
	}
	body, err := render("body", localized.Body)
	return title, body, err
}
