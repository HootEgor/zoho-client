package core

import (
	"log/slog"
	"zohoclient/internal/lib/sl"
)

// ProcessCustomers fetches up to 100 OpenCart customers without a zoho_id,
// upserts each into the Zoho Contacts module, and records the returned Zoho
// record ID back on oc_customer.zoho_id. Customers that fail (e.g. missing
// both email and phone) stay unmarked and will be retried on the next tick.
func (c *Core) ProcessCustomers() {
	log := c.log.With(sl.Module("customers"))

	total, synced, err := c.repo.CountCustomers()
	if err != nil {
		log.With(sl.Err(err)).Warn("count customers")
	}

	rows, err := c.repo.GetNewCustomers()
	if err != nil {
		log.With(sl.Err(err)).Error("fetch customers")
		return
	}
	if len(rows) == 0 {
		return
	}

	log.Info("processing customers",
		slog.Int("count", len(rows)),
		slog.Int64("total", total),
		slog.Int64("synced", synced),
	)

	for _, row := range rows {
		id, err := c.zoho.UpsertContact(row.Details)
		if err != nil {
			log.With(
				slog.Int64("customer_id", row.CustomerID),
				slog.String("email", row.Details.Email),
				sl.Err(err),
			).Error("upsert contact")
			continue
		}
		if err = c.repo.ChangeCustomerZohoId(row.CustomerID, id); err != nil {
			log.With(
				slog.Int64("customer_id", row.CustomerID),
				slog.String("zoho_id", id),
				sl.Err(err),
			).Error("update customer zoho_id")
		}
	}
}
