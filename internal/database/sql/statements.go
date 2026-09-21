package sql

import (
	"database/sql"
	"fmt"
	"strings"
)

func (s *MySql) prepareStmt(name, query string) (*sql.Stmt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// если уже есть — возвращаем
	if stmt, ok := s.statements[name]; ok {
		return stmt, nil
	}

	// подготавливаем новый
	stmt, err := s.db.Prepare(query)
	if err != nil {
		return nil, fmt.Errorf("prepare statement [%s]: %w", name, err)
	}

	s.statements[name] = stmt
	return stmt, nil
}

func (s *MySql) closeStmt() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, stmt := range s.statements {
		_ = stmt.Close()
		delete(s.statements, name)
	}
}

func (s *MySql) stmtUpdateOrderStatus() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %sorder SET 
                   date_modified = ?,  
                   order_status_id = ?
                   WHERE order_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("updateOrderStatus", query)
}

func (s *MySql) stmtUpdateOrderZohoId() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %sorder SET 
                   date_modified = ?,  
                   zoho_id = ?
                   WHERE order_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("updateOrderZohoId", query)
}

// orderColumns is the SELECT list every oc_order query shares, in the order scanOrderFromRows
// expects. The wf_payment_* columns are written by whichever payment module the shop runs; a site
// that syncs no payments (site.features.payments = false) may not have them at all, so they are
// left out entirely rather than selected and discarded.
func (s *MySql) orderColumns() string {
	cols := []string{
		"order_id",
		"order_status_id",
		"date_added",
		"firstname",
		"lastname",
		"email",
		"telephone",
		"customer_group_id",
		"custom_field",
		"shipping_country",
		"shipping_postcode",
		"shipping_city",
		"shipping_address_1",
		"shipping_zone",
		"shipping_zone_id",
		"currency_code",
		"currency_value",
		"total",
		"comment",
		"zoho_id",
	}
	if s.site.Payments {
		cols = append(cols,
			"wf_payment_status",
			"wf_payment_id",
			"wf_payment_amount",
			"wf_payment_session",
		)
	}
	cols = append(cols, "shipping_code", "shipping_method")
	return strings.Join(cols, ",\n\t\t\t")
}

func (s *MySql) stmtSelectOrderStatus() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			%s
		 FROM %sorder
		 WHERE order_status_id = ?
		 	AND (zoho_id = '' OR zoho_id IS NULL)
		 	AND date_modified > ?
		 LIMIT %d`,
		s.orderColumns(),
		s.prefix,
		s.site.BatchLimit,
	)
	return s.prepareStmt("selectOrderStatus", query)
}

func (s *MySql) stmtUpdateProductZohoId() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %sproduct SET zoho_id = ? WHERE product_uid = ?`,
		s.prefix,
	)
	return s.prepareStmt("updateProductZohoId", query)
}

// stmtSelectOrdersSynced selects orders placed in a date window that already carry a real Zoho
// Sales Order id, so an already-synced record can be audited or repaired. The "[B2B]" and "[SKIP]"
// sentinels are excluded — neither names a Zoho record.
func (s *MySql) stmtSelectOrdersSynced() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			%s
		 FROM %sorder
		 WHERE date_added >= ? AND date_added < ?
			AND zoho_id IS NOT NULL AND zoho_id <> '' AND zoho_id <> '[B2B]' AND zoho_id <> '[SKIP]'
		 ORDER BY order_id`,
		s.orderColumns(),
		s.prefix,
	)
	return s.prepareStmt("selectOrdersSynced", query)
}

func (s *MySql) stmtSelectOrderId() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			%s
		 FROM %sorder
		 WHERE order_id = ?`,
		s.orderColumns(),
		s.prefix,
	)
	return s.prepareStmt("stmtSelectOrderId", query)
}

func (s *MySql) stmtSelectOrderTotals() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			op.title,
			op.value
		 FROM %sorder_total op
		 WHERE op.order_id = ? AND op.code=?`,
		s.prefix,
	)
	return s.prepareStmt("selectOrderTotals", query)
}

func (s *MySql) stmtSelectOrderProducts() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			pd.name,
			op.product_id,
			ifnull(pr.product_uid, "") as uid,
			ifnull(pr.zoho_id, "") as zoho_id,
			op.total,
			op.price,
			op.tax,
			op.quantity,
			op.model,
			pr.price as master_price
		 FROM %sorder_product op
		 JOIN %sproduct_description pd ON op.product_id = pd.product_id
		 JOIN %sproduct pr ON op.product_id = pr.product_id
		 WHERE op.order_id = ? AND pd.language_id = %d`,
		s.prefix, s.prefix, s.prefix, s.site.LanguageID,
	)
	return s.prepareStmt("selectOrderProducts", query)
}

func (s *MySql) stmtSelectOrderByZohoId() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			%s
		 FROM %sorder
		 WHERE zoho_id = ?`,
		s.orderColumns(),
		s.prefix,
	)
	return s.prepareStmt("selectOrderByZohoId", query)
}

func (s *MySql) stmtSelectOrderZohoModifiedTime() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT zoho_modified_time FROM %sorder WHERE order_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("selectOrderZohoModifiedTime", query)
}

func (s *MySql) stmtUpdateOrderZohoModifiedTime() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %sorder SET zoho_modified_time = ? WHERE order_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("updateOrderZohoModifiedTime", query)
}

func (s *MySql) stmtUpdateOrderZohoPaymentId() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %sorder SET zoho_payment_id = ? WHERE order_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("updateOrderZohoPaymentId", query)
}

func (s *MySql) stmtUpdateOrderZohoPayment() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %sorder SET zoho_payment_id = ?, zoho_payment_status = ? WHERE order_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("updateOrderZohoPayment", query)
}

func (s *MySql) stmtSetOrderZohoPaymentStatus() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %sorder SET zoho_payment_status = ? WHERE order_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("setOrderZohoPaymentStatus", query)
}

// stmtSelectOrdersPendingPayment finds orders that are already in Zoho but have payment
// data from wfsync that hasn't been synced to Zoho yet.
func (s *MySql) stmtSelectOrdersPendingPayment() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			%s
		 FROM %sorder
		 WHERE zoho_id != '' AND zoho_id IS NOT NULL
		 	AND wf_payment_status != '' AND wf_payment_status IS NOT NULL
		 	AND (zoho_payment_id = '' OR zoho_payment_id IS NULL)
		 LIMIT 10`,
		s.orderColumns(),
		s.prefix,
	)
	return s.prepareStmt("selectOrdersPendingPayment", query)
}

// stmtSelectOrdersPendingPaymentUpdate finds orders whose Zoho Payments record already
// exists (real id, not the "[ERR]" sentinel) but whose wf_payment_status has advanced
// past the value last synced to Zoho (zoho_payment_status).
func (s *MySql) stmtSelectOrdersPendingPaymentUpdate() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			%s
		 FROM %sorder
		 WHERE zoho_id != '' AND zoho_id IS NOT NULL
		 	AND zoho_payment_id != '' AND zoho_payment_id IS NOT NULL
		 	AND zoho_payment_id != '%s'
		 	AND wf_payment_status != '' AND wf_payment_status IS NOT NULL
		 	AND wf_payment_status != zoho_payment_status
		 	AND date_modified > DATE_SUB(NOW(), INTERVAL 60 DAY)
		 LIMIT 10`,
		s.orderColumns(),
		s.prefix,
		paymentZohoIdError,
	)
	return s.prepareStmt("selectOrdersPendingPaymentUpdate", query)
}

// paymentZohoIdError is the sentinel stored in oc_order.zoho_payment_id when a payment
// cannot be created in Zoho due to a non-transient error. It must match the value used
// in impl/core (paymentZohoIdError); it is duplicated here because the core package
// imports this package and the reference cannot go the other way.
const paymentZohoIdError = "[ERR]"

// stmtUpdateOrderPaymentState writes the wf_payment_* family this service does not normally own.
// Only reached on a tranzzo shop after a zoho_managed takeover, when the shop's own module has
// stepped back from the columns and Zoho drives the payment. Definitions and units must match what
// the module wrote before it stepped back: status one of its four words, amount in minor units.
func (s *MySql) stmtUpdateOrderPaymentState() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %sorder SET
			wf_payment_status = ?,
			wf_payment_id = ?,
			wf_payment_amount = ?
		 WHERE order_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("updateOrderPaymentState", query)
}

// stmtInsertTranzzoTask enqueues one task for the UA shop's OpenCart Tranzzo module.
//
// Only method, code, root_code and payload are set: status ('N'), attempt (0) and date_insert
// (CURRENT_TIMESTAMP) all have database defaults that are already what a fresh task needs, and
// naming them here would be a second place to keep in step with the module's schema.
//
// code is not part of the contract the module documented to us, but its own dedup index is
// (status, method, code) and findActiveTask() matches on all three — so filling it lets a repeat
// of the same event be recognised rather than queued twice.
func (s *MySql) stmtInsertTranzzoTask() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`INSERT INTO %stranzzo_queue (method, code, root_code, payload) VALUES (?, ?, ?, ?)`,
		s.prefix,
	)
	return s.prepareStmt("insertTranzzoTask", query)
}

func (s *MySql) stmtSelectOrderSimpleFields() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT IFNULL(%s, '') FROM %sorder_simple_fields WHERE order_id = ?`,
		s.site.PostTerminalField,
		s.prefix,
	)
	return s.prepareStmt("selectOrderSimpleFields", query)
}
