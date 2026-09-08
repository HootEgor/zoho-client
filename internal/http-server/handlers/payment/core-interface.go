package payment

import (
	"zohoclient/entity"
)

type Core interface {
	UpdatePayments(update *entity.ApiPaymentUpdate) error
}
