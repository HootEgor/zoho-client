package database

import (
	"database/sql"
	"errors"
	"fmt"
	_ "github.com/go-sql-driver/mysql" // MySQL driver
	"log/slog"
	"sync"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/lib/sl"
)

type MySql struct {
	db         *sql.DB
	prefix     string
	structure  map[string]map[string]Column
	statements map[string]*sql.Stmt
	mu         sync.Mutex
	log        *slog.Logger
}

func NewSQLClient(conf *config.Config, log *slog.Logger) (*MySql, error) {
	if !conf.SQL.Enabled {
		return nil, fmt.Errorf("SQL client is disabled in configuration")
	}
	connectionURI := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		conf.SQL.UserName, conf.SQL.Password, conf.SQL.HostName, conf.SQL.Port, conf.SQL.Database)
	db, err := sql.Open("mysql", connectionURI)
	if err != nil {
		return nil, fmt.Errorf("sql connect: %w", err)
	}

	// try ping three times with 30 seconds interval; wait for database to start
	for i := 0; i < 3; i++ {
		if err = db.Ping(); err == nil {
			break
		}
		if i == 2 {
			return nil, fmt.Errorf("ping database: %w", err)
		}
		time.Sleep(30 * time.Second)
	}

	db.SetMaxOpenConns(50)           // макс. кол-во открытых соединений
	db.SetMaxIdleConns(10)           // макс. кол-во "неактивных" соединений в пуле
	db.SetConnMaxLifetime(time.Hour) // время жизни соединения

	sdb := &MySql{
		db:         db,
		prefix:     conf.SQL.Prefix,
		structure:  make(map[string]map[string]Column),
		statements: make(map[string]*sql.Stmt),
		log:        log,
	}

	if err = sdb.addColumnIfNotExists("product", "zoho_id", "VARCHAR(64) NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}
	if err = sdb.addColumnIfNotExists("order", "zoho_id", "VARCHAR(64) NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}

	return sdb, nil
}

func (s *MySql) Close() {
	s.closeStmt()
	_ = s.db.Close()
}

func (s *MySql) Stats() string {
	stats := s.db.Stats()
	return fmt.Sprintf("open: %d, inuse: %d, idle: %d, stmts: %d, structure: %d",
		stats.OpenConnections,
		stats.InUse,
		stats.Idle,
		len(s.statements),
		len(s.structure))
}

func (s *MySql) OrderSearchId(orderId int64) (*entity.OCOrder, error) {
	stmt, err := s.stmtSelectOrder()
	if err != nil {
		return nil, err
	}
	var order entity.OCOrder
	err = stmt.QueryRow(orderId).Scan(
		&order.OrderID,
		&order.InvoiceNo,
		&order.InvoicePrefix,
		&order.StoreID,
		&order.StoreName,
		&order.StoreURL,
		&order.CustomerID,
		&order.CustomerGroupID,
		&order.Firstname,
		&order.Lastname,
		&order.Email,
		&order.Telephone,
		&order.CustomField,
		&order.PaymentFirstname,
		&order.PaymentLastname,
		&order.PaymentCompany,
		&order.PaymentAddress1,
		&order.PaymentAddress2,
		&order.PaymentCity,
		&order.PaymentPostcode,
		&order.PaymentCountry,
		&order.PaymentCountryID,
		&order.PaymentZone,
		&order.PaymentZoneID,
		&order.PaymentAddressFormat,
		&order.PaymentCustomField,
		&order.PaymentMethod,
		&order.PaymentCode,
		&order.ShippingFirstname,
		&order.ShippingLastname,
		&order.ShippingCompany,
		&order.ShippingAddress1,
		&order.ShippingAddress2,
		&order.ShippingCity,
		&order.ShippingPostcode,
		&order.ShippingCountry,
		&order.ShippingCountryID,
		&order.ShippingZone,
		&order.ShippingZoneID,
		&order.ShippingAddressFormat,
		&order.ShippingCustomField,
		&order.ShippingMethod,
		&order.ShippingCode,
		&order.Comment,
		&order.Total,
		&order.OrderStatusID,
		&order.AffiliateID,
		&order.Commission,
		&order.MarketingID,
		&order.Tracking,
		&order.LanguageID,
		&order.CurrencyID,
		&order.CurrencyCode,
		&order.CurrencyValue,
		&order.IP,
		&order.ForwardedIP,
		&order.UserAgent,
		&order.AcceptLanguage,
		&order.DateAdded,
		&order.DateModified,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // no order found
		}
		return nil, fmt.Errorf("scan order: %w", err)
	}
	return &order, nil
}

func (s *MySql) OrderSearchStatus(statusId int64) ([]int64, error) {
	stmt, err := s.stmtSelectOrderStatus()
	if err != nil {
		return nil, err
	}
	rows, err := stmt.Query(statusId)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var orderIds []int64
	for rows.Next() {
		var orderId int64
		if err = rows.Scan(&orderId); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		orderIds = append(orderIds, orderId)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	return orderIds, nil
}

func (s *MySql) GetNewOrders() ([]entity.OCOrder, error) {
	statuses := []int64{
		entity.OrderStatusPending,
	}

	var orders []entity.OCOrder
	for _, status := range statuses {
		orderIds, err := s.OrderSearchStatus(status)
		if err != nil {
			s.log.With(
				sl.Err(err),
			).Debug("order search status")
			continue
		}

		for _, orderId := range orderIds {
			order, err := s.OrderSearchId(orderId)
			if err != nil {
				s.log.With(
					sl.Err(err),
				).Debug("order search id")
				continue
			}
			if order != nil {
				orders = append(orders, *order)
			}
		}
	}

	return orders, nil
}

func (s *MySql) ChangeOrderStatus(orderId, orderStatusId int64) error {
	stmt, err := s.stmtUpdateOrderStatus()
	if err != nil {
		return err
	}

	dateModified := time.Now()
	_, err = stmt.Exec(dateModified, orderStatusId, orderId)
	if err != nil {
		return fmt.Errorf("update: %v", err)
	}
	return nil
}

func (s *MySql) ChangeOrderZohoId(orderId int64, zohoId string) error {
	stmt, err := s.stmtUpdateOrderZohoId()
	if err != nil {
		return err
	}

	dateModified := time.Now()
	_, err = stmt.Exec(dateModified, zohoId, orderId)
	if err != nil {
		return fmt.Errorf("update zoho_id: %w", err)
	}
	return nil
}

func (s *MySql) GetOrderProducts(orderId int64) ([]entity.Product, error) {
	query := fmt.Sprintf(`
		SELECT 
		    ifnull(p.product_uid, "") as uid,
			p.zoho_id,
			op.quantity,
			p.price
		FROM 
			%[1]sorder_product op
		LEFT JOIN 
			%[1]sproduct p ON op.product_id = p.product_id
		WHERE 
			op.order_id = ?
	`, s.prefix)

	rows, err := s.db.Query(query, orderId)
	if err != nil {
		return nil, fmt.Errorf("query products: %w", err)
	}
	defer rows.Close()

	var products []entity.Product
	for rows.Next() {
		var product entity.Product
		if err = rows.Scan(
			&product.UID,
			&product.ZohoId,
			&product.Quantity,
			&product.Price,
		); err != nil {
			return nil, fmt.Errorf("scan zoho_id: %w", err)
		}
		products = append(products, product)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return products, nil
}

func (s *MySql) UpdateProductZohoId(productUID, zohoId string) error {
	stmt, err := s.stmtUpdateProductZohoId()
	if err != nil {
		return err
	}

	_, err = stmt.Exec(zohoId, productUID)
	if err != nil {
		return fmt.Errorf("update product zoho_id: %w", err)
	}
	return nil
}
