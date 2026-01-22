package core

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/database/sql"
	"zohoclient/internal/lib/sl"
)

type Repository interface {
	GetNewOrders() ([]*entity.CheckoutParams, error)
	OrderSearchId(orderId int64) (string, *entity.CheckoutParams, error)
	OrderSearchByZohoId(zohoId string) (int64, *entity.CheckoutParams, error)
	ChangeOrderStatus(orderId, orderStatusId int64, comment string) error
	ChangeOrderZohoId(orderId int64, zohoId string) error
	OrderTotal(orderId int64, code string) (string, float64, error)

	// UpdateOrderWithTransaction Transaction-based order update
	UpdateOrderWithTransaction(data sql.OrderUpdateTransaction) error

	UpdateProductZohoId(productUID string, zohoId string) error
	GetProductZohoIdByUid(productUID string) (string, error)
	GetProductByUid(productUID string) (name string, zohoId string, err error)

	UpdateOrderTracking(orderId int64, tracking string) error
	GetOrderTracking(orderId int64) (string, error)
}

type ProductRepository interface {
	GetProductZohoID(productUID string) (string, error)
}

type Zoho interface {
	RefreshToken() error
	CreateContact(contactData *entity.ClientDetails) (string, error)
	CreateOrder(orderData entity.ZohoOrder) (string, error)
	CreateB2BOrder(orderData entity.ZohoOrderB2B) (string, error)
	AddItemsToOrder(orderID string, items []*entity.OrderedItem) (string, error)
	AddItemsToOrderB2B(orderID string, items []*entity.Good) (string, error)
	UpdateOrder(orderData entity.ZohoOrder, id string) error
}

type MessageService interface {
	SendEventMessage(msg *entity.EventMessage) error
}

type MongoRepository interface {
	SaveOrderVersion(orderID int64, payload string) error
	DeleteExpired() (int64, error)
	GetSSLastProcessedTime(chatID string) (time.Time, error)
	SetSSLastProcessedTime(chatID string, t time.Time) error
	GetAllSSLastProcessedTimes() (map[string]time.Time, error)
}

type SmartSenderService interface {
	GetAllChats() ([]entity.SSChat, error)
	GetMessagesAfterTime(chatID string, afterTime time.Time) ([]entity.SSMessage, error)
}

type ZohoFunctionsService interface {
	SendMessages(contactID string, messages []entity.ZohoMessageItem) error
}

type Core struct {
	repo               Repository
	prodRepo           ProductRepository
	mongoRepo          MongoRepository
	zoho               Zoho
	ms                 MessageService
	shippingItemZohoId string
	statuses           map[int]string
	statusesB2B        map[int]string
	authKey            string
	keys               map[string]string
	keysMu             sync.RWMutex
	log                *slog.Logger
	stopCh             chan struct{}

	// SmartSender integration
	smartSender       SmartSenderService
	zohoFunctions     ZohoFunctionsService
	ssLastProcessed   map[string]time.Time
	ssLastProcessedMu sync.RWMutex
	ssPollInterval    time.Duration
	// When SmartSender API signals rate limiting, pause processing until this time
	ssRateLimitUntil time.Time
	ssRateLimitMu    sync.RWMutex
	// If processing was interrupted by rate limit, resume from this chat ID on next run
	ssResumeFromChatID string
	ssResumeMu         sync.RWMutex
}

func New(log *slog.Logger, conf config.Config) *Core {
	return &Core{
		log: log.With(sl.Module("core")),
		statuses: map[int]string{
			entity.OrderStatusNew:                "Нове",
			entity.OrderStatusPayed:              "Оплачено, формування ТТН",
			entity.OrderStatusPrepareForShipping: "Перевірка та збір",
		},
		statusesB2B: map[int]string{
			entity.OrderStatusNew:                "Нове замовлення",
			entity.OrderStatusPayed:              "Оплачено формування ТТН",
			entity.OrderStatusPrepareForShipping: "Передано на збір",
		},
		authKey:         conf.Listen.ApiKey,
		keys:            make(map[string]string),
		stopCh:          make(chan struct{}),
		ssLastProcessed: make(map[string]time.Time),
	}
}

func (c *Core) Stop() {
	close(c.stopCh)
}

func (c *Core) SetRepository(repo Repository) {
	c.repo = repo

	// Load shipping item zoho_id from database
	zohoId, err := repo.GetProductZohoIdByUid(entity.ShippingItemUid)
	if err != nil {
		c.log.Warn("failed to load shipping item zoho_id", sl.Err(err))
	} else if zohoId != "" {
		c.shippingItemZohoId = zohoId
		c.log.Debug("shipping item zoho_id loaded", slog.String("zoho_id", zohoId))
	}
}

func (c *Core) SetProductRepository(prodRepo ProductRepository) {
	c.prodRepo = prodRepo
}

func (c *Core) SetMongoRepository(mongoRepo MongoRepository) {
	c.mongoRepo = mongoRepo
}

func (c *Core) SetZoho(zoho Zoho) {
	c.zoho = zoho
}

func (c *Core) SetMessageService(ms MessageService) {
	c.ms = ms
}

func (c *Core) SetSmartSenderService(ss SmartSenderService) {
	c.smartSender = ss
}

func (c *Core) SetZohoFunctionsService(zf ZohoFunctionsService) {
	c.zohoFunctions = zf
}

func (c *Core) SetSmartSenderPollInterval(interval time.Duration) {
	c.ssPollInterval = interval
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

	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-c.stopCh:
				c.log.Info("order processing stopped")
				return
			default:
				c.ProcessOrders()
			}

			select {
			case <-c.stopCh:
				c.log.Info("order processing stopped")
				return
			case <-ticker.C:
			}
		}
	}()

	// Separate goroutine for MongoDB cleanup (runs every 12 hours)
	go func() {
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()

		// Run cleanup once at startup
		c.cleanupExpiredMongoOrders()

		for {
			select {
			case <-c.stopCh:
				return
			case <-ticker.C:
				c.cleanupExpiredMongoOrders()
			}
		}
	}()

	// SmartSender processing goroutine
	c.startSmartSenderProcessing()
}

// cleanupExpiredMongoOrders removes old order records from MongoDB.
func (c *Core) cleanupExpiredMongoOrders() {
	if c.mongoRepo == nil {
		return
	}

	_, err := c.mongoRepo.DeleteExpired()
	if err != nil {
		c.log.With(sl.Err(err)).Warn("failed to cleanup expired mongo orders")
	}
}
