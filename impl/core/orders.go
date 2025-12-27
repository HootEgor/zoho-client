package core

import (
	"fmt"
	"log/slog"
	"math"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

const (
	ZohoLocation    = "Польша"
	ZohoOrderSource = "OpenCart"

	ChunkSize = 100
)

// PushOrderToZoho fetches an order by ID from the database and pushes it to Zoho CRM.
// Returns the Zoho order ID on success.
func (c *Core) PushOrderToZoho(orderId int64) (string, error) {
	log := c.log.With(slog.Int64("order_id", orderId))

	_, order, err := c.repo.OrderSearchId(orderId)
	if err != nil {
		return "", fmt.Errorf("order search: %w", err)
	}

	//if existingZohoId != "" {
	//	log.Info("order already has zoho_id", slog.String("zoho_id", existingZohoId))
	//	return existingZohoId, nil
	//}

	if err := order.Validate(); err != nil {
		return "", fmt.Errorf("order validation: %w", err)
	}

	log = log.With(
		slog.String("name", fmt.Sprintf("%s : %s", order.ClientDetails.FirstName, order.ClientDetails.LastName)),
		slog.String("country", order.ClientDetails.Country),
		slog.String("currency", order.Currency),
		slog.Float64("total", round2(order.Total)),
		slog.String("tax", order.TaxTitle),
		slog.Float64("tax_value", round2(order.TaxValue)),
	)

	contactID, err := c.zoho.CreateContact(order.ClientDetails)
	if err != nil {
		log.With(
			slog.String("email", order.ClientDetails.Email),
			slog.String("phone", order.ClientDetails.Phone),
			sl.Err(err),
		).Error("create contact")
		return "", fmt.Errorf("create contact: %w", err)
	}

	if e := hasEmptyUid(order.LineItems); e != nil {
		return "", fmt.Errorf("product without UID: %w", e)
	}

	if e := hasEmptyZohoID(order.LineItems); e != nil {
		c.processProductsWithoutZohoID(order.LineItems)

		if ee := hasEmptyZohoID(order.LineItems); ee != nil {
			return "", fmt.Errorf("product without Zoho ID: %w", ee)
		}
	}

	zohoOrder, chunkedItems := c.buildZohoOrder(order, contactID)

	orderZohoId, err := c.zoho.CreateOrder(zohoOrder)
	if err != nil {
		return "", fmt.Errorf("create Zoho order: %w", err)
	}

	for i, chunk := range chunkedItems {
		_, err = c.zoho.AddItemsToOrder(orderZohoId, chunk)
		if err != nil {
			log.With(
				sl.Err(err),
				slog.Int("chunk", i+1),
			).Error("add items to order")
			return "", fmt.Errorf("add items to order (chunk %d): %w", i+1, err)
		}
	}

	log.With(slog.String("zoho_id", orderZohoId)).Info("order pushed to Zoho")

	err = c.repo.ChangeOrderZohoId(orderId, orderZohoId)
	if err != nil {
		log.With(sl.Err(err)).Error("update order zoho_id")
		return orderZohoId, fmt.Errorf("update zoho_id in database: %w", err)
	}

	return orderZohoId, nil
}

// ProcessOrders fetches all new orders from the database and pushes them to Zoho CRM.
// B2B orders are skipped and marked with "[B2B]" zoho_id. Orders with missing product
// UIDs or Zoho IDs are skipped until the missing data is available.
func (c *Core) ProcessOrders() {
	orders, err := c.repo.GetNewOrders()
	if err != nil {
		c.log.With(sl.Err(err)).Error("failed to get new orders")
		return
	}

	for _, order := range orders {

		log := c.log.With(
			slog.Int64("order_id", order.OrderId),
			slog.String("currency", order.Currency),
			slog.String("tax", order.TaxTitle),
			slog.Float64("total", round2(order.Total)),
			slog.Float64("tax_value", round2(order.TaxValue)),
		)

		if order.ClientDetails == nil {
			log.Warn("no client details")
			continue
		}
		if order.LineItems == nil || len(order.LineItems) == 0 {
			log.Warn("no line items")
			continue
		}

		log = log.With(
			slog.String("name", fmt.Sprintf("%s : %s", order.ClientDetails.FirstName, order.ClientDetails.LastName)),
			slog.String("country", order.ClientDetails.Country),
		)

		if order.ClientDetails.IsB2B() {
			log.With(
				slog.Int64("group_id", order.ClientDetails.GroupId),
			).Debug("b2b client; order skipped")
			_ = c.repo.ChangeOrderZohoId(order.OrderId, "[B2B]")
			continue
		}

		contactID, err := c.zoho.CreateContact(order.ClientDetails)
		if err != nil {
			log.With(
				slog.String("email", order.ClientDetails.Email),
				slog.String("phone", order.ClientDetails.Phone),
				sl.Err(err),
			).Error("create contact")
			_ = c.repo.ChangeOrderStatus(order.OrderId, entity.OrderStatusCanceled, fmt.Sprintf("Zoho: %v", err))
			continue
		}

		if e := hasEmptyUid(order.LineItems); e != nil {
			log.With(
				sl.Err(e),
			).Warn("order has product(s) without UID")
			continue
		}

		if e := hasEmptyZohoID(order.LineItems); e != nil {
			// Try to fetch Zoho IDs for products without them
			c.processProductsWithoutZohoID(order.LineItems)

			// Check if there are still products without Zoho IDs
			if ee := hasEmptyZohoID(order.LineItems); ee != nil {
				log.With(
					sl.Err(ee),
				).Error("order has product(s) without Zoho ID")
				continue // leave in queue
			}
		}

		zohoOrder, chunkedItems := c.buildZohoOrder(order, contactID)

		orderZohoId, err := c.zoho.CreateOrder(zohoOrder)
		if err != nil {
			log.With(sl.Err(err)).Error("create Zoho order")
			continue
		}

		for i, chunk := range chunkedItems {
			_, err = c.zoho.AddItemsToOrder(orderZohoId, chunk)
			if err != nil {
				log.With(
					sl.Err(err),
					slog.Int("chunk", i+1),
				).Error("add items to order")
				break
			}
		}
		if err != nil {
			continue
		}

		log.With(
			slog.String("zoho_id", orderZohoId),
		).Info("order created")

		err = c.repo.ChangeOrderZohoId(order.OrderId, orderZohoId)
		if err != nil {
			log.With(sl.Err(err)).Error("update order zoho_id")
		}
	}

}

// hasEmptyZohoID checks if any product in the slice has an empty ZohoId.
// Returns an error with product details if found, nil otherwise.
func hasEmptyZohoID(products []*entity.LineItem) error {
	for _, p := range products {
		if p.ZohoId == "" {
			return fmt.Errorf("product id=%d %s has empty zoho_id", p.Id, p.Name)
		}
	}
	return nil
}

// hasEmptyUid checks if any product in the slice has an empty UID.
// Returns an error with product details if found, nil otherwise.
func hasEmptyUid(products []*entity.LineItem) error {
	for _, p := range products {
		if p.Uid == "" {
			return fmt.Errorf("product id=%d %s has empty UID", p.Id, p.Name)
		}
	}
	return nil
}

// processProductsWithoutZohoID fetches Zoho IDs from the product repository for products
// that don't have them. Updates both the in-memory slice and the database.
func (c *Core) processProductsWithoutZohoID(products []*entity.LineItem) {
	for i, p := range products {
		if p.ZohoId == "" {
			zohoID, err := c.prodRepo.GetProductZohoID(p.Uid)
			if err != nil {
				c.log.With(
					slog.String("product", p.Name),
					slog.String("product_uid", p.Uid),
					sl.Err(err),
				).Error("get product")
				continue
			}

			if zohoID != "" {
				err = c.repo.UpdateProductZohoId(p.Uid, zohoID)
				if err != nil {
					c.log.With(
						slog.String("product", p.Name),
						slog.String("product_uid", p.Uid),
						slog.String("zoho_id", zohoID),
						sl.Err(err),
					).Error("update product")
					continue
				}
				products[i].ZohoId = zohoID
			}
		}
	}
}

// buildOrderedItem converts a LineItem to a Zoho OrderedItem with the given discount percentage.
func buildOrderedItem(lineItem *entity.LineItem, discountP float64) entity.OrderedItem {
	totalWithDiscount := round2(lineItem.Qty * lineItem.Price * discountP / 100)
	return entity.OrderedItem{
		Product: entity.ZohoProduct{
			ID: lineItem.ZohoId,
		},
		Quantity: int64(lineItem.Qty),
		//Discount:
		DiscountP: discountP,
		ListPrice: lineItem.Price,
		Total:     totalWithDiscount,
	}
}

// buildZohoOrder constructs a ZohoOrder from CheckoutParams. Returns the order and any
// additional item chunks that exceed ChunkSize (100 items) for subsequent API calls.
func (c *Core) buildZohoOrder(oc *entity.CheckoutParams, contactID string) (entity.ZohoOrder, [][]*entity.OrderedItem) {
	var orderedItems []entity.OrderedItem
	var chunkedItems [][]*entity.OrderedItem
	var chunk []*entity.OrderedItem

	discount, discountP := oc.Discount()
	discountP = round0(discountP)

	for _, d := range oc.LineItems {
		item := buildOrderedItem(d, discountP)

		// First ChunkSize items go into orderedItems (initial order creation)
		if len(orderedItems) < ChunkSize {
			orderedItems = append(orderedItems, item)
		} else {
			// Subsequent items go into chunks for AddItemsToOrder calls
			itemCopy := item
			chunk = append(chunk, &itemCopy)
			if len(chunk) >= ChunkSize {
				chunkedItems = append(chunkedItems, chunk)
				chunk = []*entity.OrderedItem{}
			}
		}
	}

	// Don't forget remaining items in the last chunk
	if len(chunk) > 0 {
		chunkedItems = append(chunkedItems, chunk)
	}

	return entity.ZohoOrder{
		ContactName:        entity.ContactName{ID: contactID},
		OrderedItems:       orderedItems,
		Discount:           round2(discount),
		DiscountP:          round0(discountP),
		Description:        oc.Comment,
		CustomerNo:         "",
		ShippingState:      "",
		Tax:                0,
		VAT:                round0(oc.TaxRate()),
		GrandTotal:         round2(oc.Total),
		SubTotal:           round2(oc.Total - oc.TaxValue),
		Currency:           oc.Currency,
		BillingCountry:     oc.ClientDetails.Country,
		Carrier:            "",
		Status:             c.statuses[oc.StatusId],
		SalesCommission:    0,
		DueDate:            time.Now().Format("2006-01-02"),
		BillingStreet:      oc.ClientDetails.Street,
		Adjustment:         0,
		TermsAndConditions: "Standard terms apply.",
		BillingCode:        oc.ClientDetails.ZipCode,
		ProductDetails:     nil,
		Subject:            fmt.Sprintf("Order #%d", oc.OrderId),
		IDsite:             fmt.Sprintf("%d", oc.OrderId),
		Location:           ZohoLocation,
		OrderSource:        ZohoOrderSource,
	}, chunkedItems
}

// round0 rounds a float64 to the nearest integer, converting negative values to positive.
func round0(value float64) float64 {
	if value < 0 {
		value = -value
	}
	return math.Round(value)
}

// round2 rounds a float64 to 2 decimal points
func round2(value float64) float64 {
	if value < 0 {
		value = -value
	}
	return math.Round(value*100) / 100
}
