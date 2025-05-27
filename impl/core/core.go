package core

import (
	"fmt"
	"log/slog"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

type Repository interface {
	GetNewOrders() ([]entity.OCOrder, error)
	ChangeOrderStatus(orderId, orderStatusId int64, zohoId string) error

	GetOrderProducts(orderId int64) ([]entity.Product, error)
	UpdateProductZohoId(productUID string, zohoId string) error
}

type ProductRepository interface {
	GetProductZohoID(productUID string) (string, error)
}

type Zoho interface {
	RefreshToken() error
	CreateContact(contactData entity.Contact) (string, error)
	CreateOrder(orderData entity.ZohoOrder) (string, error)
	UpdateOrder(orderData entity.ZohoOrder, id string) error
}

type MessageService interface {
	SendEventMessage(msg *entity.EventMessage) error
}

type Core struct {
	repo       Repository
	prodRepo   ProductRepository
	zoho       Zoho
	ms         MessageService
	orderQueue []entity.OCOrder
	statuses   map[int]string
	log        *slog.Logger
}

func New(log *slog.Logger) *Core {
	return &Core{
		log: log.With(sl.Module("core")),
		statuses: map[int]string{
			entity.OrderStatusNew: "Нове",
		},
	}
}

func (c *Core) SetRepository(repo Repository) {
	c.repo = repo
}

func (c *Core) SetProductRepository(prodRepo ProductRepository) {
	c.prodRepo = prodRepo
}

func (c *Core) SetZoho(zoho Zoho) {
	c.zoho = zoho
}

func (c *Core) SetMessageService(ms MessageService) {
	c.ms = ms
}

func (c *Core) SendEvent(message *entity.EventMessage) (interface{}, error) {
	if c.ms == nil {
		return nil, fmt.Errorf("not set MessageService")
	}
	return nil, c.ms.SendEventMessage(message)
}

func (c *Core) Start() {
	if c.zoho == nil {
		c.log.Error("Zoho service not set")
		return
	}

	if c.repo == nil {
		c.log.Error("Repository service not set")
		return
	}

	if c.prodRepo == nil {
		c.log.Error("ProductRepository service not set")
		return
	}

	// Refresh token every 55 minutes
	go func() {
		ticker := time.NewTicker(55 * time.Minute)
		defer ticker.Stop()

		for {
			err := c.zoho.RefreshToken()
			if err != nil {
				c.log.Error("failed to refresh Zoho token", slog.String("error", err.Error()))
			}
			<-ticker.C
		}
	}()

	c.ProcessOrders()

	//Process orders every 1 minute
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			//c.ProcessOrders()
			<-ticker.C
		}
	}()
}
