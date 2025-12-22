package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/lib/sl"

	_ "github.com/go-sql-driver/mysql" // MySQL driver
)

const (
	totalCodeShipping = "shipping"
	//totalCodeDiscount = "discount"
	totalCodeTax = "tax"
	//totalCodeTotal    = "total"
	customFieldNip = "2"
	locationCode   = "Europe/Warsaw"
)

type MySql struct {
	db         *sql.DB
	loc        *time.Location
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

	loc, err := time.LoadLocation(locationCode)
	if err != nil {
		return nil, fmt.Errorf("load location: %w", err)
	}
	sdb.loc = loc

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

func (s *MySql) GetNewOrders() ([]*entity.CheckoutParams, error) {
	statuses := []int{
		entity.OrderStatusNew,
		entity.OrderStatusPayed,
		entity.OrderStatusPrepareForShipping,
	}

	from := time.Now().Add(-30 * 24 * time.Hour)

	var orders []*entity.CheckoutParams
	for _, status := range statuses {
		params, err := s.OrderSearchStatus(status, from)
		if err != nil {
			s.log.With(
				sl.Err(err),
			).Debug("order search status")
			continue
		}

		for _, order := range params {
			orders = append(orders, order)
		}
	}

	return orders, nil
}

func (s *MySql) ChangeOrderStatus(orderId, orderStatusId int64, comment string) error {
	stmt, err := s.stmtUpdateOrderStatus()
	if err != nil {
		return err
	}

	dateModified := time.Now()
	_, err = stmt.Exec(dateModified, orderStatusId, orderId)
	if err != nil {
		return fmt.Errorf("update: %v", err)
	}

	if comment != "" {
		// add order history record
		rec := map[string]interface{}{
			"order_id":        orderId,
			"order_status_id": orderStatusId,
			"notify":          0,
			"comment":         comment,
			"date_added":      dateModified,
		}
		_, err = s.insert("order_history", rec)
		if err != nil {
			return fmt.Errorf("insert order history: %w", err)
		}
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

func (s *MySql) OrderSearchStatus(statusId int, from time.Time) ([]*entity.CheckoutParams, error) {
	stmt, err := s.stmtSelectOrderStatus()
	if err != nil {
		return nil, err
	}
	rows, err := stmt.Query(statusId, from)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer func(rows *sql.Rows) {
		_ = rows.Close()
	}(rows)

	var orders []*entity.CheckoutParams
	for rows.Next() {

		var order entity.CheckoutParams
		var client entity.ClientDetails
		var customField string
		var total float64

		if err = rows.Scan(
			&order.OrderId,
			&order.Created,
			&client.FirstName,
			&client.LastName,
			&client.Email,
			&client.Phone,
			&client.GroupId,
			&customField,
			&client.Country,
			&client.ZipCode,
			&client.City,
			&client.Street,
			&order.Currency,
			&order.CurrencyValue,
			&total,
			&order.Comment,
		); err != nil {
			return nil, err
		}

		// client data
		_ = client.ParseTaxId(customFieldNip, strings.TrimPrefix(strings.TrimSuffix(customField, " "), " "))
		order.ClientDetails = &client
		order.TrimSpaces()
		// order summary
		order.Total = int64(math.Round(total * order.CurrencyValue * 100))
		order.Source = entity.SourceOpenCart
		order.StatusId = statusId

		orders = append(orders, &order)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	// add line items and shipping costs to each order
	for _, order := range orders {
		_, err = s.addOrderData(order.OrderId, order)
		if err != nil {
			return nil, fmt.Errorf("add order data: %w", err)
		}
	}

	return orders, nil
}

func (s *MySql) OrderSearchId(orderId int64) (string, *entity.CheckoutParams, error) {
	stmt, err := s.stmtSelectOrderId()
	if err != nil {
		return "", nil, err
	}
	rows, err := stmt.Query(orderId)
	if err != nil {
		return "", nil, fmt.Errorf("query: %w", err)
	}
	defer func(rows *sql.Rows) {
		_ = rows.Close()
	}(rows)

	var zohoId string

	var order entity.CheckoutParams
	if rows.Next() {

		var client entity.ClientDetails
		var customField string
		var total float64

		if err = rows.Scan(
			&order.OrderId,
			&order.StatusId,
			&order.Created,
			&client.FirstName,
			&client.LastName,
			&client.Email,
			&client.Phone,
			&customField,
			&client.Country,
			&client.ZipCode,
			&client.City,
			&client.Street,
			&order.Currency,
			&order.CurrencyValue,
			&total,
			&order.Comment,
			&zohoId,
		); err != nil {
			return "", nil, err
		}

		// client data
		_ = client.ParseTaxId(customFieldNip, strings.TrimPrefix(strings.TrimSuffix(customField, " "), " "))
		order.ClientDetails = &client
		order.TrimSpaces()
		// order summary
		order.Total = int64(math.Round(total * order.CurrencyValue * 100))
		order.Source = entity.SourceOpenCart
	}

	if err = rows.Err(); err != nil {
		return "", nil, err
	}

	params, err := s.addOrderData(orderId, &order)

	return zohoId, params, err
}

func (s *MySql) OrderProducts(orderId int64, currencyValue float64, ignoreTax bool) ([]*entity.LineItem, error) {
	stmt, err := s.stmtSelectOrderProducts()
	if err != nil {
		return nil, err
	}
	rows, err := stmt.Query(orderId)
	if err != nil {
		return nil, err
	}
	defer func(rows *sql.Rows) {
		_ = rows.Close()
	}(rows)

	var products []*entity.LineItem
	for rows.Next() {
		var product entity.LineItem
		var total float64
		var tax float64
		var price float64
		if err = rows.Scan(
			&product.Name,
			&product.Id,
			&product.Uid,
			&product.ZohoId,
			&total,
			&price,
			&tax,
			&product.Qty,
			&product.Sku,
		); err != nil {
			return nil, err
		}
		if ignoreTax {
			tax = 0
		}
		if product.Qty > 0 && price > 0 {
			// standard OpenCart logic
			priceVAT := price + tax
			// OpenCart module 'OrderPRO' contains defected logic of tax calculation, so try to detect variants
			vatCheck := tax / price
			if vatCheck > 0.25 {
				// 'tax' contains row total VAT
				priceVAT = price + tax/float64(product.Qty)
			}
			product.Price = int64(math.Round(priceVAT * currencyValue * 100))
			products = append(products, &product)
		}
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return products, nil
}

func (s *MySql) OrderTotal(orderId int64, code string, currencyValue float64) (string, int64, error) {
	stmt, err := s.stmtSelectOrderTotals()
	if err != nil {
		return "", 0, err
	}
	rows, err := stmt.Query(orderId, code)
	if err != nil {
		return "", 0, err
	}
	defer func(rows *sql.Rows) {
		_ = rows.Close()
	}(rows)

	var title string
	var value float64
	for rows.Next() {
		if err = rows.Scan(
			&title,
			&value,
		); err != nil {
			return "", 0, err
		}
	}

	if err = rows.Err(); err != nil {
		return "", 0, err
	}

	return title, int64(math.Round(value * currencyValue * 100)), nil
}

// addOrderData retrieves and calculates tax, line items, and shipping costs for a specific order and updates its details.
func (s *MySql) addOrderData(orderId int64, order *entity.CheckoutParams) (*entity.CheckoutParams, error) {
	var err error
	// before adding line items and shipping costs to each order, get order tax
	order.TaxTitle, order.TaxValue, err = s.OrderTotal(orderId, totalCodeTax, order.CurrencyValue)
	if err != nil {
		return nil, fmt.Errorf("get order tax: %w", err)
	}

	// add line items and shipping costs to each order
	order.LineItems, err = s.OrderProducts(orderId, order.CurrencyValue, order.TaxValue == 0)
	if err != nil {
		return nil, fmt.Errorf("get order products: %w", err)
	}
	title, value, err := s.OrderTotal(orderId, totalCodeShipping, order.CurrencyValue)
	if err != nil {
		return nil, fmt.Errorf("get order shipping: %w", err)
	}
	if value > 0 {
		order.AddShipping(title, value)
	}
	order.RecalcWithDiscount()

	return order, nil
}

// OrderSearchByZohoId searches for an order by its Zoho ID and returns the order_id and order data.
func (s *MySql) OrderSearchByZohoId(zohoId string) (int64, *entity.CheckoutParams, error) {
	stmt, err := s.stmtSelectOrderByZohoId()
	if err != nil {
		return 0, nil, err
	}
	rows, err := stmt.Query(zohoId)
	if err != nil {
		return 0, nil, fmt.Errorf("query: %w", err)
	}
	defer func(rows *sql.Rows) {
		_ = rows.Close()
	}(rows)

	var foundZohoId string
	var order entity.CheckoutParams
	if rows.Next() {
		var client entity.ClientDetails
		var customField string
		var total float64

		if err = rows.Scan(
			&order.OrderId,
			&order.StatusId,
			&order.Created,
			&client.FirstName,
			&client.LastName,
			&client.Email,
			&client.Phone,
			&customField,
			&client.Country,
			&client.ZipCode,
			&client.City,
			&client.Street,
			&order.Currency,
			&order.CurrencyValue,
			&total,
			&order.Comment,
			&foundZohoId,
		); err != nil {
			return 0, nil, err
		}

		order.Total = int64(math.Round(total * order.CurrencyValue * 100))
	} else {
		return 0, nil, fmt.Errorf("order with zoho_id '%s' not found", zohoId)
	}

	if err = rows.Err(); err != nil {
		return 0, nil, err
	}

	return order.OrderId, &order, nil
}

// UpdateOrderItems replaces all order line items and recalculates order totals.
// Steps: 1) Delete all existing items, 2) Insert new items, 3) Recalculate with discount, 4) Update order total
func (s *MySql) UpdateOrderItems(orderId int64, items []entity.ApiOrderedItem, currencyValue float64, orderTotal float64) error {
	// Step 1: Delete all existing order items
	deleteStmt, err := s.stmtDeleteOrderProducts()
	if err != nil {
		return fmt.Errorf("prepare delete statement: %w", err)
	}

	_, err = deleteStmt.Exec(orderId)
	if err != nil {
		return fmt.Errorf("delete existing order items: %w", err)
	}

	// Step 2: Insert new order items
	insertStmt, err := s.stmtInsertOrderProduct()
	if err != nil {
		return fmt.Errorf("prepare insert statement: %w", err)
	}

	var lineItems []*entity.LineItem
	for _, item := range items {
		// Convert float price to cents (int64) for database storage
		priceInCents := int64(math.Round(item.Price * 100))
		totalInCents := int64(math.Round(item.Price * float64(item.Quantity) * 100))

		_, err = insertStmt.Exec(orderId, item.Quantity, priceInCents, totalInCents, item.ZohoID)
		if err != nil {
			return fmt.Errorf("insert order item (zoho_id: %s): %w", item.ZohoID, err)
		}

		// Build LineItem for discount calculation
		lineItems = append(lineItems, &entity.LineItem{
			ZohoId: fmt.Sprintf("%d", item.ZohoID),
			Qty:    int64(item.Quantity),
			Price:  priceInCents,
		})
	}

	// Step 3: Recalculate order with discount
	orderTotalInCents := int64(math.Round(orderTotal * 100))
	checkoutParams := &entity.CheckoutParams{
		LineItems: lineItems,
		Total:     orderTotalInCents,
		Shipping:  0, // Will be added if exists
	}

	// Add shipping if it exists in order_total table
	title, shippingValue, err := s.OrderTotal(orderId, totalCodeShipping, currencyValue)
	if err != nil {
		return fmt.Errorf("get order shipping: %w", err)
	}
	if shippingValue > 0 {
		checkoutParams.AddShipping(title, shippingValue)
	}

	// Recalculate discounts
	checkoutParams.RecalcWithDiscount()

	// Step 4: Update order items with calculated discounts
	updateStmt, err := s.stmtUpdateOrderProduct()
	if err != nil {
		return fmt.Errorf("prepare update statement: %w", err)
	}

	for _, item := range checkoutParams.LineItems {
		if item.Shipping {
			continue // Skip shipping line item
		}

		totalWithDiscount := item.Price*item.Qty - item.Discount
		_, err = updateStmt.Exec(item.Price, totalWithDiscount, item.Qty, orderId, item.ZohoId)
		if err != nil {
			return fmt.Errorf("update item discount (zoho_id: %s): %w", item.ZohoId, err)
		}
	}

	// Step 5: Update order total in the order table
	updateTotalStmt, err := s.stmtUpdateOrderTotal()
	if err != nil {
		return fmt.Errorf("prepare update total statement: %w", err)
	}

	// Convert total back to float for database (order.total is float)
	totalFloat := float64(orderTotalInCents) / (100 * currencyValue)
	_, err = updateTotalStmt.Exec(time.Now(), totalFloat, orderId)
	if err != nil {
		return fmt.Errorf("update order total: %w", err)
	}

	return nil
}
