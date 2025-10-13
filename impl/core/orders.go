package core

import (
	"fmt"
	"log/slog"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

func (c *Core) ProcessOrders() {
	newOrders, err := c.repo.GetNewOrders()
	if err != nil {
		c.log.Error("failed to get new orders", slog.String("error", err.Error()))
		return
	}

	ordersProcessed := 0

	for _, ocOrder := range newOrders {
		contact := entity.Contact{
			FirstName: ocOrder.Firstname,
			LastName:  ocOrder.Lastname,
			Email:     ocOrder.Email,
			Field2:    ocOrder.ShippingCity,
			Phone:     filterDigitsOnly(ocOrder.Telephone),
		}

		contactID, err := c.zoho.CreateContact(contact)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", ocOrder.OrderID),
				sl.Err(err),
			).Error("create contact")
			continue
		}

		orderProducts, err := c.repo.GetOrderProducts(ocOrder.OrderID)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", ocOrder.OrderID),
				sl.Err(err),
			).Error("get order products")
			continue
		}

		if hasEmptyUid(orderProducts) {
			c.log.With(
				slog.Int64("order_id", ocOrder.OrderID),
				slog.Any("products", orderProducts),
			).Warn("order has product(s) without UID, skipping")
			continue
		}

		if hasEmptyZohoID(orderProducts) {
			// Try to fetch Zoho IDs for products without them
			updatedProducts, updated := c.processProductsWithoutZohoID(orderProducts)
			orderProducts = updatedProducts

			if updated {
				c.log.With(
					slog.Int64("order_id", ocOrder.OrderID),
				).Info("updated Zoho IDs for some products")
			}

			// Check if there are still products without Zoho IDs
			if hasEmptyZohoID(orderProducts) {
				c.log.With(
					slog.Int64("order_id", ocOrder.OrderID),
				).Warn("order still has product(s) without Zoho ID, skipping")
				continue // leave in queue
			}
		}

		zohoOrder := c.buildZohoOrder(ocOrder, orderProducts, contactID)

		orderZohoId, err := c.zoho.CreateOrder(zohoOrder)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", ocOrder.OrderID),
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

		err = c.repo.ChangeOrderZohoId(ocOrder.OrderID, orderZohoId)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", ocOrder.OrderID),
				sl.Err(err),
			).Error("update order zohoid")
		}

		ordersProcessed += 1
	}

	c.log.With(
		slog.Int("processed_orders", ordersProcessed),
		slog.Int("remaining_orders", len(newOrders)-ordersProcessed),
	).Info("processed orders")
}

func hasEmptyZohoID(products []entity.Product) bool {
	for _, p := range products {
		if p.ZohoId == "" {
			return true
		}
	}
	return false
}

func hasEmptyUid(products []entity.Product) bool {
	for _, p := range products {
		if p.UID == "" {
			return true
		}
	}
	return false
}

func (c *Core) processProductsWithoutZohoID(products []entity.Product) ([]entity.Product, bool) {
	var productsUpdated bool

	for i, p := range products {
		if p.ZohoId == "" {
			zohoID, err := c.prodRepo.GetProductZohoID(p.UID)
			if err != nil {
				c.log.With(
					slog.String("product_uid", p.UID),
					sl.Err(err),
				).Error("failed to get product Zoho ID")
				continue
			}

			if zohoID != "" {
				err = c.repo.UpdateProductZohoId(p.UID, zohoID)
				if err != nil {
					c.log.With(
						slog.String("product_uid", p.UID),
						slog.String("zoho_id", zohoID),
						sl.Err(err),
					).Error("failed to update product Zoho ID")
					continue
				}

				// Update the product in the slice
				products[i].ZohoId = zohoID
				productsUpdated = true

				c.log.With(
					slog.String("product_uid", p.UID),
					slog.String("zoho_id", zohoID),
				).Info("product Zoho ID updated")
			}
		}
	}

	return products, productsUpdated
}

func filterDigitsOnly(phone string) string {
	var result []rune
	for _, ch := range phone {
		if ch >= '0' && ch <= '9' {
			result = append(result, ch)
		}
	}
	return string(result)
}

func (c *Core) buildZohoOrder(oc entity.OCOrder, products []entity.Product, contactID string) entity.ZohoOrder {
	var productDetails []entity.ProductDetail

	for _, p := range products {
		productDetails = append(productDetails, entity.ProductDetail{
			Product:     entity.ProductID{ID: p.ZohoId},
			Quantity:    p.Quantity,
			Discount:    0, // set appropriately if discount info available
			ProductDesc: p.UID,
			UnitPrice:   float64(p.Price), // set price if available
			LineTax: []entity.LineTax{
				{Name: "Common Tax", Percentage: 0},
			},
		})
	}

	orderedItems := convertToOrderedItems(productDetails)

	return entity.ZohoOrder{
		ContactName:        entity.ContactName{ID: contactID},
		OrderedItems:       orderedItems,
		Discount:           0,
		Description:        oc.Comment,
		CustomerNo:         fmt.Sprint(oc.CustomerID),
		ShippingState:      oc.ShippingZone,
		Tax:                0,
		BillingCountry:     oc.PaymentCountry,
		Carrier:            oc.ShippingMethod,
		Status:             c.statuses[entity.OrderStatusNew],
		SalesCommission:    0,
		DueDate:            time.Now().Format("2006-01-02"),
		BillingStreet:      oc.PaymentAddress1,
		Adjustment:         0,
		TermsAndConditions: "Standard terms apply.",
		BillingCode:        oc.PaymentPostcode,
		ProductDetails:     nil,
		Subject:            fmt.Sprintf("Order #%d", oc.OrderID),
	}
}

func convertToOrderedItems(details []entity.ProductDetail) []entity.OrderedItem {
	var orderedItems []entity.OrderedItem

	for _, d := range details {
		item := entity.OrderedItem{
			Product: entity.ZohoProduct{
				ID:   d.Product.ID,
				Name: d.ProductDesc, // using ProductDesc as the name
			},
			Quantity:  d.Quantity,
			Discount:  roundToTwoDecimalPlaces(d.Discount),
			DiscountP: 0,
			ListPrice: roundToTwoDecimalPlaces(d.UnitPrice),
			Total:     roundToTwoDecimalPlaces(d.UnitPrice * float64(d.Quantity)),
		}
		orderedItems = append(orderedItems, item)
	}

	return orderedItems
}

func roundToTwoDecimalPlaces(value float64) float64 {
	return float64(int(value*100)) / 100.0
}
