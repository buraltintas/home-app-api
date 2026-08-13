package notification

import (
	"context"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

type PushMessage struct {
	UserID, Title, Body string
	Data                map[string]string
	Locale              i18n.Locale
	TemplateKey         string
	TemplateParams      map[string]any
}
type PushProvider interface {
	Send(context.Context, PushMessage) error
}
