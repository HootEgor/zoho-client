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

func (c *Core) ProcessOrders() {
	orders, err := c.repo.GetNewOrders()
	if err != nil {
		c.log.Error("failed to get new orders", slog.String("error", err.Error()))
		return
	}

	zohoId, longOrder, err := c.repo.OrderSearchId(7725)
	if err != nil {
		c.log.Error("failed to get order", slog.String("error", err.Error()))
	}

	if longOrder != nil && zohoId == "[B2B]" {
		orders = append(orders, longOrder)
	} else {
		c.log.With(
			slog.String("zoho_id", zohoId),
		).Info("order found")
	}

	for _, order := range orders {

		order.DiscountP = roundFloat(order.DiscountP)

		log := c.log.With(
			slog.Int64("order_id", order.OrderId),
			slog.String("currency", order.Currency),
			slog.String("tax", order.TaxTitle),
			slog.Float64("discount", order.DiscountP),
			slog.Int64("total", order.Total),
			slog.Int64("tax_value", order.TaxValue),
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
			//slog.String("email", order.ClientDetails.Email),
			slog.String("country", order.ClientDetails.Country),
		)

		//if order.ClientDetails.IsB2B() {
		//	log.With(
		//		slog.Int64("group_id", order.ClientDetails.GroupId),
		//	).Debug("b2b client; order skipped")
		//	_ = c.repo.ChangeOrderZohoId(order.OrderId, "[B2B]")
		//	continue
		//}

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
			//log.With(
			//	sl.Err(err),
			//).Error("create Zoho order")
			continue
		}

		for _, chunk := range chunkedItems {
			_, err = c.zoho.AddItemsToOrder(orderZohoId, chunk)
			if err != nil {
				break
			}
		}
		if err != nil {
			continue
		}

		log.With(
			slog.String("zoho_id", orderZohoId),
		).Info("order created")

		//err = c.repo.ChangeOrderStatus(ocOrder.OrderID, entity.OrderStatusProcessing)
		//if err != nil {
		//	c.log.With(
		//		slog.Int64("order_id", ocOrder.OrderID),
		//		sl.Err(err),
		//	).Error("update order status")
		//}

		err = c.repo.ChangeOrderZohoId(order.OrderId, orderZohoId)
		if err != nil {
			log.With(
				sl.Err(err),
			).Error("update order zoho_id")
		}
	}

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

				// Update the product in the slice
				products[i].ZohoId = zohoID

				//c.log.With(
				//	slog.String("product", p.Name),
				//	slog.String("product_uid", p.Uid),
				//	slog.String("zoho_id", zohoID),
				//).Debug("product updated")
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

func (c *Core) buildZohoOrder(oc *entity.CheckoutParams, contactID string) (entity.ZohoOrder, [][]*entity.OrderedItem) {

	var orderedItems []entity.OrderedItem

	var chunkedItems [][]*entity.OrderedItem
	var chunk []*entity.OrderedItem

	for _, d := range oc.LineItems {
		item := entity.OrderedItem{
			Product: entity.ZohoProduct{
				ID: d.ZohoId,
				//Name: d.UID, // using UID as the name
			},
			Quantity:  d.Qty,
			Discount:  roundInt(d.Discount),
			DiscountP: roundFloat(d.DiscountP),
			ListPrice: roundInt(d.Price),
			Total:     roundInt(d.Price*d.Qty - d.Discount),
		}

		if len(orderedItems) >= ChunkSize {
			if len(chunk) >= ChunkSize {
				chunkedItems = append(chunkedItems, chunk)
				chunk = []*entity.OrderedItem{}
			} else {
				chunk = append(chunk, &item)
			}
		} else {
			orderedItems = append(orderedItems, item)
		}

	}

	return entity.ZohoOrder{
		ContactName:        entity.ContactName{ID: contactID},
		OrderedItems:       orderedItems,
		Discount:           roundInt(oc.Discount),
		DiscountP:          oc.DiscountP,
		Description:        oc.Comment,
		CustomerNo:         "", //fmt.Sprint(oc.CustomerID),
		ShippingState:      "",
		Tax:                0,
		VAT:                float64(oc.TaxRate()),
		GrandTotal:         roundInt(oc.Total),
		SubTotal:           roundInt(oc.Total - oc.TaxValue),
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

func roundInt(value int64) float64 {
	return float64(value) / 100.0
}

func roundFloat(value float64) float64 {
	if value < 0 {
		value = -value
	}
	return math.Round(value) //*100) / 100
}
