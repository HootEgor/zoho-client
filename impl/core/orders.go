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
				slog.Int("order_id", ocOrder.OrderID),
				sl.Err(err),
			).Error("create contact")
			remainingOrders = append(remainingOrders, ocOrder)
			continue
		}

		orderProducts, err := c.repo.GetOrderProducts(ocOrder.OrderID)
		if err != nil {
			c.log.With(
				slog.Int("order_id", ocOrder.OrderID),
				sl.Err(err),
			).Error("get order products")
			remainingOrders = append(remainingOrders, ocOrder)
			continue
		}

		if hasEmptyZohoID(orderProducts) {
			c.log.Warn("order has product(s) without Zoho ID, skipping", slog.Int("order_id", ocOrder.OrderID))
			remainingOrders = append(remainingOrders, ocOrder)
			continue // leave in queue
		}

		zohoOrder := buildZohoOrder(ocOrder, orderProducts, contactID)

		_, err = c.zoho.CreateOrder(zohoOrder)
		if err != nil {
			c.log.Error("failed to create Zoho order", slog.Int("order_id", ocOrder.OrderID), slog.String("error", err.Error()))
			remainingOrders = append(remainingOrders, ocOrder)
			continue
		}

		err = c.repo.ChangeOrderStatus(ocOrder.OrderID, entity.OrderStatusApproved)
		if err != nil {
			c.log.Error("failed to update order status", slog.Int("order_id", ocOrder.OrderID), slog.String("error", err.Error()))
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

func buildZohoOrder(oc entity.OCOrder, products []entity.Product, contactID string) entity.ZohoOrder {
	var productDetails []entity.ProductDetail

	for _, p := range products {
		productDetails = append(productDetails, entity.ProductDetail{
			Product:     entity.ProductID{ID: p.ZohoId},
			Quantity:    p.Quantity,
			Discount:    0, // set appropriately if discount info available
			ProductDesc: p.Model,
			UnitPrice:   0, // set price if available
			LineTax: []entity.LineTax{
				{Name: "Default Tax", Percentage: 0},
			},
		})
	}

	return entity.ZohoOrder{
		ContactName:        entity.ContactName{ID: contactID},
		OrderedItems:       []entity.OrderedItem{}, // Optional
		Discount:           0,
		Description:        oc.Comment,
		CustomerNo:         fmt.Sprint(oc.CustomerID),
		ShippingState:      oc.ShippingZone,
		Tax:                0,
		BillingCountry:     oc.PaymentCountry,
		Carrier:            oc.ShippingMethod,
		Status:             "Pending",
		SalesCommission:    0,
		DueDate:            time.Now().Format("2006-01-02"),
		BillingStreet:      oc.PaymentAddress1,
		Adjustment:         0,
		TermsAndConditions: "Standard terms apply.",
		BillingCode:        oc.PaymentPostcode,
		ProductDetails:     productDetails,
		Subject:            fmt.Sprintf("Order #%d", oc.OrderID),
	}
}
