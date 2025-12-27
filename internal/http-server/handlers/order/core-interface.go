package order

import (
	"zohoclient/entity"
)

type Core interface {
	UpdateOrder(orderDetails *entity.ApiOrder) error
	PushOrderToZoho(orderId int64) (string, error)
}
