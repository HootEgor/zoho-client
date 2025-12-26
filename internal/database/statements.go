package database

import (
	"database/sql"
	"fmt"
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

func (s *MySql) stmtSelectOrderStatus() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			order_id,
			date_added,
			firstname,
			lastname,
			email,
			telephone,
			customer_group_id,
			custom_field,
			shipping_country,
			shipping_postcode,
			shipping_city,
			shipping_address_1,
			currency_code,
			currency_value,
			total,
			comment
		 FROM %sorder
		 WHERE order_status_id = ? 
		 	AND (zoho_id = '' OR zoho_id IS NULL)
		 	AND date_modified > ?
		 LIMIT 10`,
		s.prefix,
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

func (s *MySql) stmtSelectOrderId() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			order_id,
			order_status_id,
			date_added,
			firstname,
			lastname,
			email,
			telephone,
			custom_field,
			shipping_country,
			shipping_postcode,
			shipping_city,
			shipping_address_1,
			currency_code,
			currency_value,
			total,
			comment,
			zoho_id
		 FROM %sorder
		 WHERE order_id = ?`,
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
			op.model
		 FROM %sorder_product op
		 JOIN %sproduct_description pd ON op.product_id = pd.product_id
		 JOIN %sproduct pr ON op.product_id = pr.product_id
		 WHERE op.order_id = ? AND pd.language_id = 2`,
		s.prefix, s.prefix, s.prefix,
	)
	return s.prepareStmt("selectOrderProducts", query)
}

func (s *MySql) stmtSelectOrderByZohoId() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			order_id,
			order_status_id,
			date_added,
			firstname,
			lastname,
			email,
			telephone,
			custom_field,
			shipping_country,
			shipping_postcode,
			shipping_city,
			shipping_address_1,
			currency_code,
			currency_value,
			total,
			comment,
			zoho_id
		 FROM %sorder
		 WHERE zoho_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("selectOrderByZohoId", query)
}
