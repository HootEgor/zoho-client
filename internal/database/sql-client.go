package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
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
	totalCodeCoupon   = "coupon"
	totalCodeTax      = "tax"
	totalCodeTotal    = "total"
	subTotalCode      = "sub_total"
	discountCode      = "discount"
	customFieldNip    = "2"
	locationCode      = "Europe/Warsaw"
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

// Stats returns database info only if there are connections inUse
func (s *MySql) Stats() string {
	stats := s.db.Stats()
	if stats.InUse > 0 {
		return fmt.Sprintf("open: %d, inuse: %d, idle: %d, stmts: %d, structure: %d",
			stats.OpenConnections,
			stats.InUse,
			stats.Idle,
			len(s.statements),
			len(s.structure))
	}
	return ""
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

func (s *MySql) UpdateOrderTracking(orderId int64, tracking string) error {
	stmt, err := s.stmtUpdateOrderTracking()
	if err != nil {
		return err
	}

	_, err = stmt.Exec(tracking, orderId)
	if err != nil {
		return fmt.Errorf("update tracking: %w", err)
	}
	return nil
}

func (s *MySql) GetOrderTracking(orderId int64) (string, error) {
	stmt, err := s.stmtSelectOrderTracking()
	if err != nil {
		return "", err
	}

	var tracking string
	err = stmt.QueryRow(orderId).Scan(&tracking)
	if err != nil {
		return "", fmt.Errorf("query tracking: %w", err)
	}
	return tracking, nil
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

func (s *MySql) GetProductZohoIdByUid(productUID string) (string, error) {
	query := fmt.Sprintf("SELECT zoho_id FROM %sproduct WHERE product_uid = ?", s.prefix)

	var zohoId string
	err := s.db.QueryRow(query, productUID).Scan(&zohoId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("query product zoho_id: %w", err)
	}
	return zohoId, nil
}

// GetProductByUid returns product name and zoho_id by product_uid.
// Returns empty strings if product not found.
func (s *MySql) GetProductByUid(productUID string) (name string, zohoId string, err error) {
	query := fmt.Sprintf(`
		SELECT pd.name, p.zoho_id
		FROM %sproduct p
		JOIN %sproduct_description pd ON p.product_id = pd.product_id
		WHERE p.product_uid = ? AND pd.language_id = 2
	`, s.prefix, s.prefix)

	err = s.db.QueryRow(query, productUID).Scan(&name, &zohoId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("query product by uid: %w", err)
	}
	return name, zohoId, nil
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
		order, _, err := s.scanOrderFromRows(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
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

	if !rows.Next() {
		return "", nil, fmt.Errorf("order with id %d not found", orderId)
	}

	order, zohoId, err := s.scanOrderFromRows(rows)
	if err != nil {
		return "", nil, err
	}

	if err = rows.Err(); err != nil {
		return "", nil, err
	}

	params, err := s.addOrderData(orderId, order)

	return zohoId, params, err
}

func (s *MySql) orderProducts(orderId int64) ([]*entity.LineItem, error) {
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
		if err = rows.Scan(
			&product.Name,
			&product.Id,
			&product.Uid,
			&product.ZohoId,
			&product.Total,
			&product.Price,
			&product.Tax,
			&product.Qty,
			&product.Sku,
		); err != nil {
			return nil, err
		}
		products = append(products, &product)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return products, nil
}

func (s *MySql) OrderTotal(orderId int64, code string) (string, float64, error) {
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

	return title, value, nil
}

// GetOrderProductTotals queries the sum of total and tax columns from order_product table for a given order.
// Returns sums in cents as stored in the database.
func (s *MySql) GetOrderProductTotals(orderId int64) (totalSum int64, taxSum int64, error error) {
	query := fmt.Sprintf("SELECT COALESCE(SUM(total), 0), COALESCE(SUM(tax), 0) FROM %sorder_product WHERE order_id = ?", s.prefix)

	err := s.db.QueryRow(query, orderId).Scan(&totalSum, &taxSum)
	if err != nil {
		return 0, 0, fmt.Errorf("query order product totals: %w", err)
	}

	return totalSum, taxSum, nil
}

// addOrderData retrieves tax, line items, shipping and coupon for a specific order.
func (s *MySql) addOrderData(orderId int64, order *entity.CheckoutParams) (*entity.CheckoutParams, error) {
	var err error
	// get sub total
	_, order.SubTotal, err = s.OrderTotal(orderId, subTotalCode)
	if err != nil {
		return nil, fmt.Errorf("get order sub total: %w", err)
	}
	// get order tax
	order.TaxTitle, order.TaxValue, err = s.OrderTotal(orderId, totalCodeTax)
	if err != nil {
		return nil, fmt.Errorf("get order tax: %w", err)
	}
	//get discount
	order.DiscountTitle, order.Discount, err = s.OrderTotal(orderId, discountCode)
	if err != nil {
		return nil, fmt.Errorf("get order discount: %w", err)
	}
	// get shipping
	order.ShippingTitle, order.Shipping, err = s.OrderTotal(orderId, totalCodeShipping)
	if err != nil {
		return nil, fmt.Errorf("get order shipping: %w", err)
	}
	// get coupon
	order.CouponTitle, order.Coupon, err = s.OrderTotal(orderId, totalCodeCoupon)
	if err != nil {
		return nil, fmt.Errorf("get coupon: %w", err)
	}
	// add line items
	order.LineItems, err = s.orderProducts(orderId)
	if err != nil {
		return nil, fmt.Errorf("get order products: %w", err)
	}

	return order, nil
}

// scanOrderFromRows scans a single row into CheckoutParams and returns the zoho_id.
// The rows must have columns in this exact order:
// order_id, order_status_id, date_added, firstname, lastname, email, telephone,
// customer_group_id, custom_field, shipping_country, shipping_postcode, shipping_city,
// shipping_address_1, currency_code, currency_value, total, comment, zoho_id
func (s *MySql) scanOrderFromRows(rows *sql.Rows) (*entity.CheckoutParams, string, error) {
	var order entity.CheckoutParams
	var client entity.ClientDetails
	var customField string
	var zohoId string

	if err := rows.Scan(
		&order.OrderId,
		&order.StatusId,
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
		&order.Total,
		&order.Comment,
		&zohoId,
	); err != nil {
		return nil, "", err
	}

	_ = client.ParseTaxId(customFieldNip, strings.TrimPrefix(strings.TrimSuffix(customField, " "), " "))
	order.ClientDetails = &client
	order.ClientDetails.TrimSpaces()
	order.Source = entity.SourceOpenCart

	return &order, zohoId, nil
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

	if !rows.Next() {
		return 0, nil, fmt.Errorf("order with zoho_id '%s' not found", zohoId)
	}

	order, _, err := s.scanOrderFromRows(rows)
	if err != nil {
		return 0, nil, err
	}

	if err = rows.Err(); err != nil {
		return 0, nil, err
	}

	params, err := s.addOrderData(order.OrderId, order)
	if err != nil {
		return 0, nil, fmt.Errorf("add order data: %w", err)
	}

	return order.OrderId, params, nil
}

// OrderProductData represents the data needed to insert a product line item into an order.
// All monetary values are in cents (int64) to avoid floating point precision issues.
type OrderProductData struct {
	ZohoID       string
	Quantity     int
	PriceInCents int64 // Per-unit price, in cents
	TotalInCents int64 // Line total, in cents
	TaxInCents   int64 // Unit tax, in cents
}

// OrderUpdateTransaction encapsulates all data needed for a complete order update within a transaction
type OrderUpdateTransaction struct {
	OrderID       int64
	Items         []OrderProductData
	CurrencyValue float64
	OrderTotal    int64
	Totals        OrderTotalsData
}

// OrderTotalsData contains all order_total entries to be updated
type OrderTotalsData struct {
	SubTotal      int64 // in cents
	Tax           int64 // in cents
	TaxTitle      string
	Discount      int64 // in cents
	DiscountTitle string
	Shipping      int64 // in cents
	ShippingTitle string
	Coupon        int64
	CouponTitle   string
	Total         int64 // in cents
}

// UpdateOrderWithTransaction performs a complete order update within a single transaction.
// This ensures atomicity - either all changes succeed or all are rolled back.
// Steps: 1) Delete items, 2) Insert new items, 3) Update order.total, 4) Update order_total entries, 5) Add order_history
func (s *MySql) UpdateOrderWithTransaction(data OrderUpdateTransaction) error {
	// Begin transaction
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Get current order status for order_history
	var orderStatusId int64
	selectStatusQuery := fmt.Sprintf("SELECT order_status_id FROM %sorder WHERE order_id = ?", s.prefix)
	err = tx.QueryRow(selectStatusQuery, data.OrderID).Scan(&orderStatusId)
	if err != nil {
		return fmt.Errorf("get order status: %w", err)
	}

	// Step 1: Delete all existing order items
	deleteQuery := fmt.Sprintf("DELETE FROM %sorder_product WHERE order_id = ?", s.prefix)
	_, err = tx.Exec(deleteQuery, data.OrderID)
	if err != nil {
		return fmt.Errorf("delete existing order items: %w", err)
	}

	// Step 2: Insert new order items
	insertQuery := fmt.Sprintf(`
		INSERT INTO %sorder_product (order_id, product_id, name, model, quantity, price, total, tax, reward, sku, upc, ean, jan, isbn, mpn, location, weight, discount_type, discount_amount)
		SELECT ?, p.product_id, pd.name, p.model, ?, ?, ?, ?, 0, p.sku, p.upc, p.ean, p.jan, p.isbn, p.mpn, p.location, p.weight, '', 0
		FROM %sproduct p
		JOIN %sproduct_description pd ON p.product_id = pd.product_id
		WHERE p.zoho_id = ? AND pd.language_id = 2
	`, s.prefix, s.prefix, s.prefix)

	for _, item := range data.Items {
		priceFloat := float64(item.PriceInCents) / 100.0
		totalFloat := float64(item.TotalInCents) / 100.0
		taxFloat := float64(item.TaxInCents) / 100.0

		res, err := tx.Exec(insertQuery, data.OrderID, item.Quantity, priceFloat, totalFloat, taxFloat, item.ZohoID)
		if err != nil {
			return fmt.Errorf("insert order item (zoho_id: %s): %w", item.ZohoID, err)
		}
		rowsAffected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("get rows affected: %w", err)
		}
		if rowsAffected < 1 {
			return fmt.Errorf("product not found in database (zoho_id: %s)", item.ZohoID)
		}
	}

	// Step 3: Update order.total in the order table
	now := time.Now()
	updateQuery := fmt.Sprintf("UPDATE %sorder SET date_modified = ?, total = ? WHERE order_id = ?", s.prefix)
	totalFloat := (float64(data.OrderTotal) / 100) / data.CurrencyValue
	_, err = tx.Exec(updateQuery, now, totalFloat, data.OrderID)
	if err != nil {
		return fmt.Errorf("update order total: %w", err)
	}

	// Step 4: Update all order_total entries
	// First, reset all totals to zero
	resetTotalsQuery := fmt.Sprintf("UPDATE %sorder_total SET value = 0 WHERE order_id = ?", s.prefix)
	_, err = tx.Exec(resetTotalsQuery, data.OrderID)
	if err != nil {
		return fmt.Errorf("reset order totals: %w", err)
	}

	// Then update each total by code
	updateTotalQuery := fmt.Sprintf("UPDATE %sorder_total SET value = ? WHERE order_id = ? AND code = ?", s.prefix)

	totalsToUpdate := []struct {
		code  string
		value int64
	}{
		{subTotalCode, data.Totals.SubTotal},
		{totalCodeTax, data.Totals.Tax},
		{discountCode, data.Totals.Discount},
		{totalCodeShipping, data.Totals.Shipping},
		{totalCodeCoupon, data.Totals.Coupon},
		{totalCodeTotal, data.Totals.Total},
	}

	for _, t := range totalsToUpdate {
		valueFloat := float64(t.value) / 100.0
		_, err = tx.Exec(updateTotalQuery, valueFloat, data.OrderID, t.code)
		if err != nil {
			return fmt.Errorf("update order_total (code: %s): %w", t.code, err)
		}
	}

	// Step 5: Add order_history record
	historyQuery := fmt.Sprintf(`
		INSERT INTO %sorder_history (order_id, order_status_id, notify, comment, date_added)
		VALUES (?, ?, 0, ?, ?)
	`, s.prefix)
	comment := fmt.Sprintf("Order updated from zoho, total = %.2f", totalFloat)
	_, err = tx.Exec(historyQuery, data.OrderID, orderStatusId, comment, now)
	if err != nil {
		return fmt.Errorf("insert order history: %w", err)
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
