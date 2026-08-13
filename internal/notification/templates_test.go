package notification

import (
	"strings"
	"testing"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

func TestNotificationTemplatesCoverAllLocales(t *testing.T) {
	keys := []string{TemplateFollowReceived, TemplatePostLiked, TemplatePostCommented, TemplateFavoriteStoreActivity}
	for _, locale := range i18n.Supported() {
		if len(templates[locale]) != len(keys) {
			t.Fatalf("template key count for %s = %d", locale, len(templates[locale]))
		}
		for _, key := range keys {
			title, body, err := RenderTemplate(locale, key, map[string]any{"actor": "Ada", "store": "IKEA"})
			if err != nil || title == "" || body == "" || strings.Contains(body, "{{") {
				t.Fatalf("locale=%s key=%s title=%q body=%q err=%v", locale, key, title, body, err)
			}
		}
	}
}
