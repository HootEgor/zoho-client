package core

import (
	"fmt"
	"log/slog"
	"time"
	"zohoapi/entity"
	"zohoapi/internal/lib/sl"
)

func (c *Core) ProcessOrders() {
	newOrders, err := c.repo.GetNewOrders()
	if err != nil {
		c.log.Error("failed to get new orders", slog.String("error", err.Error()))
		return
	}

	for _, newOrder := range newOrders {
		exists := false
		for _, queuedOrder := range c.orderQueue {
			if newOrder.OrderID == queuedOrder.OrderID {
				exists = true
				break
			}
		}
		if !exists {
			c.orderQueue = append(c.orderQueue, newOrder)
		}
	}

	var remainingOrders []entity.OCOrder

	for _, ocOrder := range c.orderQueue {
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
			remainingOrders = append(remainingOrders, ocOrder)
			continue
		}

		orderProducts, err := c.repo.GetOrderProducts(ocOrder.OrderID)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", ocOrder.OrderID),
				sl.Err(err),
			).Error("get order products")
			remainingOrders = append(remainingOrders, ocOrder)
			continue
		}

		if hasEmptyZohoID(orderProducts) {
			c.log.With(
				slog.Int64("order_id", ocOrder.OrderID),
			).Warn("order has product(s) without Zoho ID, skipping")
			remainingOrders = append(remainingOrders, ocOrder)
			continue // leave in queue
		}

		zohoOrder := c.buildZohoOrder(ocOrder, orderProducts, contactID)

		orderZohoId, err := c.zoho.CreateOrder(zohoOrder)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", ocOrder.OrderID),
				sl.Err(err),
			).Error("create Zoho order")
			remainingOrders = append(remainingOrders, ocOrder)
			continue
		}

		err = c.repo.ChangeOrderStatus(ocOrder.OrderID, entity.OrderStatusApproved, orderZohoId)
		if err != nil {
			c.log.With(
				slog.Int64("order_id", ocOrder.OrderID),
				sl.Err(err),
			).Error("update order status")
		}
	}

	c.orderQueue = remainingOrders
}

func hasEmptyZohoID(products []entity.Product) bool {
	for _, p := range products {
		if p.ZohoId == "" {
			return true
		}
	}
	return false
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
			ProductDesc: p.Model,
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
			Discount:  d.Discount,
			DiscountP: 0,
			ListPrice: d.UnitPrice,
			Total:     d.UnitPrice * float64(d.Quantity),
		}
		orderedItems = append(orderedItems, item)
	}

	return orderedItems
}
