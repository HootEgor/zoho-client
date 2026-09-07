package health

import (
	"zohoclient/entity"
)

type Core interface {
	Status() entity.ServiceStatus
}
