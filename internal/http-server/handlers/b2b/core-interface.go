package b2b

import "zohoclient/entity"

// Core defines the interface for B2B webhook business logic
type Core interface {
	ProcessB2BWebhook(payload *entity.B2BWebhookPayload) (string, error)
}
