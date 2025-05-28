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
			order_id
		 FROM %sorder
		 WHERE order_status_id = ?
		 LIMIT 10`,
		s.prefix,
	)
	return s.prepareStmt("selectOrderStatus", query)
}

func (s *MySql) stmtSelectOrder() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
            order_id,
            invoice_no,
            invoice_prefix,
            store_id,
            store_name,
            store_url,
            customer_id,
            customer_group_id,
            firstname,
            lastname,
            email,
            telephone,
            custom_field,
            payment_firstname,
            payment_lastname,
            payment_company,
            payment_address_1,
            payment_address_2,
            payment_city,
            payment_postcode,
            payment_country,
            payment_country_id,
            payment_zone,
            payment_zone_id,
            payment_address_format,
            payment_custom_field,
            payment_method,
            payment_code,
            shipping_firstname,
            shipping_lastname,
            shipping_company,
            shipping_address_1,
            shipping_address_2,
            shipping_city,
            shipping_postcode,
            shipping_country,
            shipping_country_id,
            shipping_zone,
            shipping_zone_id,
            shipping_address_format,
            shipping_custom_field,
            shipping_method,
            shipping_code,
            comment,
            total,
            order_status_id,
            affiliate_id,
            commission,
            marketing_id,
            tracking,
            language_id,
            currency_id,
            currency_code,
            currency_value,
            ip,
            forwarded_ip,
            user_agent,
            accept_language,
            date_added,
            date_modified
         FROM %sorder
         WHERE order_id = ?
         LIMIT 1`,
		s.prefix,
	)
	return s.prepareStmt("selectOrder", query)
}

func (s *MySql) stmtUpdateProductZohoId() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %sproduct SET zoho_id = ? WHERE product_uid = ?`,
		s.prefix,
	)
	return s.prepareStmt("updateProductZohoId", query)
}
