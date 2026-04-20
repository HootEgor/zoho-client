package sql

import (
	"database/sql"
	"fmt"
	"zohoclient/entity"
)

// CustomerRow pairs an OpenCart customer_id with its ClientDetails for the
// customer sync loop, so we can mark the row as uploaded after a successful
// Zoho upsert.
type CustomerRow struct {
	CustomerID int64
	Details    *entity.ClientDetails
}

// GetNewCustomers returns up to 100 customers that have not yet been uploaded
// to Zoho (zoho_id is empty). City and Country are pulled from the customer's
// default address via a LEFT JOIN so customers without an address still sync.
func (s *MySql) GetNewCustomers() ([]*CustomerRow, error) {
	stmt, err := s.stmtSelectNewCustomers()
	if err != nil {
		return nil, err
	}

	rows, err := stmt.Query()
	if err != nil {
		return nil, fmt.Errorf("query customers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*CustomerRow
	for rows.Next() {
		var (
			customerID int64
			firstname  string
			lastname   string
			email      string
			telephone  string
			groupID    int64
			city       string
			country    string
		)
		if err = rows.Scan(
			&customerID,
			&firstname,
			&lastname,
			&email,
			&telephone,
			&groupID,
			&city,
			&country,
		); err != nil {
			return nil, fmt.Errorf("scan customer: %w", err)
		}

		details := &entity.ClientDetails{
			FirstName: firstname,
			LastName:  lastname,
			Email:     email,
			Phone:     telephone,
			City:      city,
			Country:   country,
			GroupId:   groupID,
		}
		details.TrimSpaces()

		result = append(result, &CustomerRow{
			CustomerID: customerID,
			Details:    details,
		})
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate customers: %w", err)
	}

	return result, nil
}

// CountCustomers returns the total number of OpenCart customers and how many
// of them already have a Zoho ID stored.
func (s *MySql) CountCustomers() (total int64, synced int64, err error) {
	query := fmt.Sprintf(
		`SELECT
			COUNT(*),
			SUM(CASE WHEN zoho_id <> '' AND zoho_id IS NOT NULL THEN 1 ELSE 0 END)
		 FROM %scustomer`,
		s.prefix,
	)
	var syncedNull sql.NullInt64
	if err = s.db.QueryRow(query).Scan(&total, &syncedNull); err != nil {
		return 0, 0, fmt.Errorf("count customers: %w", err)
	}
	if syncedNull.Valid {
		synced = syncedNull.Int64
	}
	return total, synced, nil
}

// ChangeCustomerZohoId marks a customer as uploaded by storing their Zoho
// record ID on oc_customer.zoho_id.
func (s *MySql) ChangeCustomerZohoId(customerId int64, zohoId string) error {
	stmt, err := s.stmtUpdateCustomerZohoId()
	if err != nil {
		return err
	}

	if _, err = stmt.Exec(zohoId, customerId); err != nil {
		return fmt.Errorf("update customer zoho_id: %w", err)
	}
	return nil
}

func (s *MySql) stmtSelectNewCustomers() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`SELECT
			c.customer_id,
			c.firstname,
			c.lastname,
			c.email,
			c.telephone,
			c.customer_group_id,
			IFNULL(a.city, '')  AS city,
			IFNULL(co.name, '') AS country
		 FROM %scustomer c
		 LEFT JOIN %saddress a  ON a.address_id = c.address_id
		 LEFT JOIN %scountry co ON co.country_id = a.country_id
		 WHERE (c.zoho_id = '' OR c.zoho_id IS NULL)
		 ORDER BY c.customer_id
		 LIMIT 100`,
		s.prefix, s.prefix, s.prefix,
	)
	return s.prepareStmt("selectNewCustomers", query)
}

func (s *MySql) stmtUpdateCustomerZohoId() (*sql.Stmt, error) {
	query := fmt.Sprintf(
		`UPDATE %scustomer SET zoho_id = ? WHERE customer_id = ?`,
		s.prefix,
	)
	return s.prepareStmt("updateCustomerZohoId", query)
}
