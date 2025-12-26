package core

import (
	"fmt"
	"log/slog"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/database"
	"zohoclient/internal/lib/sl"
)

type Repository interface {
	GetNewOrders() ([]*entity.CheckoutParams, error)
	OrderSearchId(orderId int64) (string, *entity.CheckoutParams, error)
	OrderSearchByZohoId(zohoId string) (int64, *entity.CheckoutParams, error)
	ChangeOrderStatus(orderId, orderStatusId int64, comment string) error
	ChangeOrderZohoId(orderId int64, zohoId string) error
	OrderTotal(orderId int64, code string, currencyValue float64) (string, int64, error)

	// UpdateOrderWithTransaction Transaction-based order update (preferred method)
	UpdateOrderWithTransaction(data database.OrderUpdateTransaction) error

	// Deprecated: Use UpdateOrderWithTransaction instead
	UpdateOrderItems(orderId int64, items []database.OrderProductData, currencyValue float64, orderTotal float64) error
	UpdateOrderTotal(orderId int64, code string, valueInCents int64) error

	UpdateProductZohoId(productUID string, zohoId string) error
}

type ProductRepository interface {
	GetProductZohoID(productUID string) (string, error)
}

type Zoho interface {
	RefreshToken() error
	CreateContact(contactData *entity.ClientDetails) (string, error)
	CreateOrder(orderData entity.ZohoOrder) (string, error)
	AddItemsToOrder(orderID string, items []*entity.OrderedItem) (string, error)
	UpdateOrder(orderData entity.ZohoOrder, id string) error
}

type MessageService interface {
	SendEventMessage(msg *entity.EventMessage) error
}

type Core struct {
	repo     Repository
	prodRepo ProductRepository
	zoho     Zoho
	ms       MessageService
	statuses map[int]string
	authKey  string
	keys     map[string]string
	log      *slog.Logger
}

func New(log *slog.Logger, conf config.Config) *Core {
	return &Core{
		log: log.With(sl.Module("core")),
		statuses: map[int]string{
			entity.OrderStatusNew:                "Нове",
			entity.OrderStatusPayed:              "Оплачено, формування ТТН",
			entity.OrderStatusPrepareForShipping: "Перевірка та збір",
		},
		authKey: conf.Listen.ApiKey,
		keys:    make(map[string]string),
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

func (c *Core) SetAuthKey(key string) {
	c.authKey = key
}

func (c *Core) SendEvent(message *entity.EventMessage) (interface{}, error) {
	if c.ms == nil {
		return nil, fmt.Errorf("not set MessageService")
	}
	return nil, c.ms.SendEventMessage(message)
}

// GetStatusIdByName performs reverse lookup of status ID by status name (Ukrainian string).
// Returns the status ID or -1 if not found.
func (c *Core) GetStatusIdByName(statusName string) int {
	for id, name := range c.statuses {
		if name == statusName {
			return id
		}
	}
	return -1
}

func (c *Core) Start() {
	if c.zoho == nil {
		c.log.Error("zoho service not set")
		return
	}

	if c.repo == nil {
		c.log.Error("repository service not set")
		return
	}

	if c.prodRepo == nil {
		c.log.Error("product repository service not set")
		return
	}

	//c.log.Info("starting core service")

	// Process orders
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()

		for {
			c.ProcessOrders()
			<-ticker.C
		}
	}()
}
