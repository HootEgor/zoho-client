package core

import (
	"fmt"
	"log/slog"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

const (
	ZohoLocation    = "Польша"
	ZohoOrderSource = "OpenCart"
)

func (c *Core) ProcessOrders() {
	orders, err := c.repo.GetNewOrders()
	if err != nil {
		c.log.Error("failed to get new orders", slog.String("error", err.Error()))
		return
	}

	ordersProcessed := 0

	for _, order := range orders {

		if order.ClientDetails == nil {
			c.log.With(
				slog.Int64("order_id", order.OrderId),
			).Warn("order has no client details, skipping")
			continue
		}
		if order.LineItems == nil || len(order.LineItems) == 0 {
			c.log.With(
				slog.Int64("order_id", order.OrderId),
			).Warn("order has no line items, skipping")
			continue
		}

		if !order.ClientDetails.IsB2B() {
			continue
		}

		contactID, err := c.zoho.CreateContact(order.ClientDetails)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", order.OrderId),
				sl.Err(err),
			).Error("create contact")
			continue
		}

		if e := hasEmptyUid(order.LineItems); e != nil {
			c.log.With(
				slog.Int64("order_id", order.OrderId),
				sl.Err(e),
			).Warn("order has product(s) without UID, skipping")
			continue
		}

		if e := hasEmptyZohoID(order.LineItems); e != nil {
			// Try to fetch Zoho IDs for products without them
			c.processProductsWithoutZohoID(order.LineItems)

			// Check if there are still products without Zoho IDs
			if ee := hasEmptyZohoID(order.LineItems); ee != nil {
				c.log.With(
					slog.Int64("order_id", order.OrderId),
				).Warn("order has product(s) without Zoho ID, skipping")
				continue // leave in queue
			}
		}

		zohoOrder := c.buildZohoOrder(order, contactID)

		orderZohoId, err := c.zoho.CreateOrder(zohoOrder)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", order.OrderId),
				sl.Err(err),
			).Error("create Zoho order")
			continue
		}

		//err = c.repo.ChangeOrderStatus(ocOrder.OrderID, entity.OrderStatusProcessing)
		//if err != nil {
		//	c.log.With(
		//		slog.Int64("order_id", ocOrder.OrderID),
		//		sl.Err(err),
		//	).Error("update order status")
		//}

		err = c.repo.ChangeOrderZohoId(order.OrderId, orderZohoId)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", order.OrderId),
				sl.Err(err),
			).Error("update order zoho_id")
		}

		ordersProcessed += 1
	}

	//if ordersProcessed > 0 {
	//	c.log.With(
	//		slog.Int("processed_orders", ordersProcessed),
	//		slog.Int("remaining_orders", len(orders)-ordersProcessed),
	//	).Info("processed orders")
	//}
}

func hasEmptyZohoID(products []*entity.LineItem) error {
	for _, p := range products {
		if p.ZohoId == "" {
			return fmt.Errorf("product id=%d %s has empty zoho_id", p.Id, p.Name)
		}
	}
	return nil
}

func hasEmptyUid(products []*entity.LineItem) error {
	for _, p := range products {
		if p.Uid == "" {
			return fmt.Errorf("product id=%d %s has empty UID", p.Id, p.Name)
		}
	}
	return nil
}

func (c *Core) processProductsWithoutZohoID(products []*entity.LineItem) {

	for i, p := range products {
		if p.ZohoId == "" {
			zohoID, err := c.prodRepo.GetProductZohoID(p.Uid)
			if err != nil {
				c.log.With(
					slog.String("product", p.Name),
					slog.String("product_uid", p.Uid),
					sl.Err(err),
				).Error("failed to get product Zoho ID")
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
					).Error("failed to update product Zoho ID")
					continue
				}

				// Update the product in the slice
				products[i].ZohoId = zohoID

				c.log.With(
					slog.String("product", p.Name),
					slog.String("product_uid", p.Uid),
					slog.String("zoho_id", zohoID),
				).Info("product Zoho ID updated")
			}
		}
	}

}

//func filterDigitsOnly(phone string) string {
//	var result []rune
//	for _, ch := range phone {
//		if ch >= '0' && ch <= '9' {
//			result = append(result, ch)
//		}
//	}
//	return string(result)
//}

func (c *Core) buildZohoOrder(oc *entity.CheckoutParams, contactID string) entity.ZohoOrder {

	var orderedItems []entity.OrderedItem

	for _, d := range oc.LineItems {
		item := entity.OrderedItem{
			Product: entity.ZohoProduct{
				ID: d.ZohoId,
				//Name: d.UID, // using UID as the name
			},
			Quantity:  d.Qty,
			Discount:  roundToTwoDecimalPlaces(d.Discount),
			DiscountP: d.DiscountP,
			ListPrice: roundToTwoDecimalPlaces(d.Price),
			Total:     roundToTwoDecimalPlaces(d.Price*d.Qty - d.Discount),
		}
		orderedItems = append(orderedItems, item)
	}

	return entity.ZohoOrder{
		ContactName:        entity.ContactName{ID: contactID},
		OrderedItems:       orderedItems,
		Discount:           roundToTwoDecimalPlaces(oc.Discount),
		DiscountP:          oc.DiscountP,
		Description:        oc.Comment,
		CustomerNo:         "", //fmt.Sprint(oc.CustomerID),
		ShippingState:      "",
		Tax:                0,
		VAT:                float64(oc.TaxRate()),
		GrandTotal:         roundToTwoDecimalPlaces(oc.Total),
		SubTotal:           roundToTwoDecimalPlaces(oc.Total - oc.TaxValue),
		BillingCountry:     oc.ClientDetails.Country,
		Carrier:            "",
		Status:             c.statuses[entity.OrderStatusPayed],
		SalesCommission:    0,
		DueDate:            time.Now().Format("2006-01-02"),
		BillingStreet:      oc.ClientDetails.Street,
		Adjustment:         0,
		TermsAndConditions: "Standard terms apply.",
		BillingCode:        oc.ClientDetails.ZipCode,
		ProductDetails:     nil,
		Subject:            fmt.Sprintf("Order #%d", oc.OrderId),
		Location:           ZohoLocation,
		OrderSource:        ZohoOrderSource,
	}
}

func roundToTwoDecimalPlaces(value int64) float64 {
	return float64(value) / 100.0
}
