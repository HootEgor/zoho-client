package database

import (
	"database/sql"
	"fmt"
	_ "github.com/go-sql-driver/mysql" // MySQL driver
	"sync"
	"time"
	"zohoapi/entity"
	"zohoapi/internal/config"
)

type MySql struct {
	db         *sql.DB
	prefix     string
	structure  map[string]map[string]Column
	statements map[string]*sql.Stmt
	mu         sync.Mutex
}

func NewSQLClient(conf *config.Config) (*MySql, error) {
	if !conf.SQL.Enabled {
		return nil, nil
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
	}

	if err = sdb.addColumnIfNotExists("product", "zoho_id", "VARCHAR(64) NOT NULL"); err != nil {
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

func (s *MySql) GetNewOrders() ([]entity.OCOrder, error) {
	query := fmt.Sprintf(`
		SELECT 
			accept_language, addressLocker, affiliate_id, build_price, build_price_prefix, build_price_yes_no,
			calculated_summ, comment, comment_manager, commission, currency_code, currency_id, currency_value,
			custom_field, customer_group_id, customer_id, date_added, date_modified, delivery_price, email, fax,
			firstname, forwarded_ip, invoice_no, invoice_prefix, ip, language_id, lastname, manager_process_orders,
			marketing_id, order_id, order_status_id, parcelLocker, payment_address_1, payment_address_2,
			payment_address_format, payment_city, payment_code, payment_company, payment_country,
			payment_country_id, payment_custom_field, payment_firstname, payment_lastname, payment_method,
			payment_postcode, payment_zone, payment_zone_id, rise_product_price, rise_product_price_prefix,
			rise_product_yes_no, shipping_address_1, shipping_address_2, shipping_address_format, shipping_city,
			shipping_code, shipping_company, shipping_country, shipping_country_id, shipping_custom_field,
			shipping_firstname, shipping_lastname, shipping_method, shipping_postcode, shipping_zone,
			shipping_zone_id, store_id, store_name, store_url, telephone, text_ttn, total, tracking, user_agent
		FROM %sorder
		WHERE order_status_id IN (?, ?)
	`, s.prefix)

	rows, err := s.db.Query(query, entity.OrderStatusPending, entity.OrderStatusNew)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var orders []entity.OCOrder

	for rows.Next() {
		var order entity.OCOrder
		err := rows.Scan(
			&order.AcceptLanguage, &order.AddressLocker, &order.AffiliateID, &order.BuildPrice, &order.BuildPricePrefix, &order.BuildPriceYesNo,
			&order.CalculatedSumm, &order.Comment, &order.CommentManager, &order.Commission, &order.CurrencyCode, &order.CurrencyID, &order.CurrencyValue,
			&order.CustomField, &order.CustomerGroupID, &order.CustomerID, &order.DateAdded, &order.DateModified, &order.DeliveryPrice, &order.Email, &order.Fax,
			&order.Firstname, &order.ForwardedIP, &order.InvoiceNo, &order.InvoicePrefix, &order.IP, &order.LanguageID, &order.Lastname, &order.ManagerProcessOrders,
			&order.MarketingID, &order.OrderID, &order.OrderStatusID, &order.ParcelLocker, &order.PaymentAddress1, &order.PaymentAddress2,
			&order.PaymentAddressFormat, &order.PaymentCity, &order.PaymentCode, &order.PaymentCompany, &order.PaymentCountry,
			&order.PaymentCountryID, &order.PaymentCustomField, &order.PaymentFirstname, &order.PaymentLastname, &order.PaymentMethod,
			&order.PaymentPostcode, &order.PaymentZone, &order.PaymentZoneID, &order.RiseProductPrice, &order.RiseProductPricePrefix,
			&order.RiseProductYesNo, &order.ShippingAddress1, &order.ShippingAddress2, &order.ShippingAddressFormat, &order.ShippingCity,
			&order.ShippingCode, &order.ShippingCompany, &order.ShippingCountry, &order.ShippingCountryID, &order.ShippingCustomField,
			&order.ShippingFirstname, &order.ShippingLastname, &order.ShippingMethod, &order.ShippingPostcode, &order.ShippingZone,
			&order.ShippingZoneID, &order.StoreID, &order.StoreName, &order.StoreURL, &order.Telephone, &order.TextTTN, &order.Total, &order.Tracking, &order.UserAgent,
		)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		orders = append(orders, order)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	return orders, nil
}

func (s *MySql) ChangeOrderStatus(orderId, orderStatusId int) error {
	stmt, err := s.stmtUpdateOrderStatus()
	if err != nil {
		return err
	}

	dateModified := time.Now()
	_, err = stmt.Exec(orderStatusId, dateModified, orderId)
	if err != nil {
		return fmt.Errorf("update: %v", err)
	}
	return nil
}

func (s *MySql) GetOrderProducts(orderId int) ([]entity.Product, error) {
	query := fmt.Sprintf(`
		SELECT 
		    p.model,
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
			&product.Model,
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
