package notification

import "context"

type PushMessage struct {
	UserID, Title, Body string
	Data                map[string]string
}
type PushProvider interface {
	Send(context.Context, PushMessage) error
}
