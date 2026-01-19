package core

import (
	"fmt"
	"log/slog"
	"math"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/database/sql"
	"zohoclient/internal/lib/sl"
)

func (c *Core) UpdateOrder(orderDetails *entity.ApiOrder) error {
	log := c.log.With(sl.Module("core.UpdateOrder"))

	if orderDetails.ZohoID == "" {
		return fmt.Errorf("zoho_id is required")
	}

	// Retry logic to handle race condition: webhook may arrive before zoho_id is saved to database
	const maxRetries = 5
	const retryDelay = 200 * time.Millisecond

	var orderId int64
	var orderParams *entity.CheckoutParams
	var err error

	for attempt := 0; attempt < maxRetries; attempt++ {
		orderId, orderParams, err = c.repo.OrderSearchByZohoId(orderDetails.ZohoID)
		if err == nil {
			break
		}
		if attempt < maxRetries-1 {
			log.With(slog.Int("attempt", attempt+1)).Debug("order not found, retrying...")
			time.Sleep(retryDelay)
		}
	}
	if err != nil {
		return fmt.Errorf("order not found after %d attempts: %w", maxRetries, err)
	}

	currencyValue := orderParams.CurrencyValue

	log = log.With(
		slog.String("zoho_id", orderDetails.ZohoID),
		slog.Int64("order_id", orderId),
	)

	// Check tracking and reset to "" if not empty
	tracking, err := c.repo.GetOrderTracking(orderId)
	if err != nil {
		return fmt.Errorf("failed to get tracking: %w", err)
	}

	if tracking != "" {
		err = c.repo.UpdateOrderTracking(orderId, "")
		if err != nil {
			return fmt.Errorf("failed to reset tracking: %w", err)
		}
		log.With(slog.String("tracking", tracking)).Debug("tracking reset to empty")
		return nil
	}

	// Update status if provided (done separately before transaction)
	if orderDetails.Status != "" {
		statusId := c.GetStatusIdByName(orderDetails.Status)
		if statusId > 0 {
			log = log.With(slog.Int("status_id", statusId))
			err = c.repo.ChangeOrderStatus(orderId, int64(statusId), "Updated via API")
			if err != nil {
				return fmt.Errorf("failed to update status: %w", err)
			}
		}
	}

	// Calculate tax rate from existing order totals
	taxRate := orderParams.TaxRate() / 100

	// Calculate discount percentage from API items
	discountPercent := c.calculateDiscountPercent(orderDetails.OrderedItems)

	var itemsTotal int64
	var shippingTotal int64

	// Prepare product data with calculated tax
	productData := make([]sql.OrderProductData, 0, len(orderDetails.OrderedItems))
	for _, item := range orderDetails.OrderedItems {
		// Calculate shipping total separately
		if item.ZohoID == c.shippingItemZohoId {
			shippingTotal += int64(math.Round(item.Price * 100))
			continue
		}

		// Calculate tax per unit
		taxPerUnit := item.Price * taxRate /// (1 + taxRate)
		itemPrice := item.Price            /// (1 + taxRate)

		// Calculate line total (price × quantity, no discount)
		lineTotal := itemPrice * float64(item.Quantity)

		// Convert to cents
		productData = append(productData, sql.OrderProductData{
			ZohoID:       item.ZohoID, // Already a string, use directly
			Quantity:     item.Quantity,
			PriceInCents: int64(math.Round(itemPrice * 100)),
			TotalInCents: int64(math.Round(lineTotal * 100)),
			TaxInCents:   int64(math.Round(taxPerUnit * 100)),
		})

		itemsTotal += int64(math.Round(lineTotal * 100))
		//taxTotal += int64(math.Round(taxPerUnit*100)) * int64(item.Quantity)
	}

	// Calculate discount and final total
	discount := int64(math.Round(float64(itemsTotal) * discountPercent))

	zohoTax := int64(math.Round(orderDetails.GrandTotal*100)) - (itemsTotal + shippingTotal - discount)
	zohoTaxRate := float64(zohoTax) / float64(itemsTotal-discount)

	taxTotal := int64(math.Round(float64(itemsTotal) * zohoTaxRate * (1 - discountPercent)))
	total := itemsTotal + taxTotal + shippingTotal - discount

	coupon := int64(0)
	if orderDetails.Coupon != "" {
		coupon = discount
		discount = 0
	}

	// Execute entire update in a single transaction
	txData := sql.OrderUpdateTransaction{
		OrderID:       orderId,
		Items:         productData,
		CurrencyValue: currencyValue,
		OrderTotal:    total,
		Totals: sql.OrderTotalsData{
			SubTotal: itemsTotal,
			Tax:      taxTotal,
			Discount: discount,
			Shipping: shippingTotal,
			Total:    total,
			Coupon:   coupon,
		},
	}

	err = c.repo.UpdateOrderWithTransaction(txData)
	if err != nil {
		return fmt.Errorf("failed to update order: %w", err)
	}

	// Save order version to MongoDB
	c.saveOrderVersionToMongo(orderId, orderDetails)

	log.With(
		slog.Int64("sub_total", itemsTotal),
		slog.Int64("shipping", shippingTotal),
		slog.Float64("discountP", discountPercent),
		slog.Int64("discount", discount),
		slog.Int64("coupon", coupon),
		slog.Int64("tax_total", taxTotal),
		slog.Float64("tax_rate", taxRate),
		slog.Float64("zoho_tax_rate", zohoTaxRate),
		slog.Int64("total", total),
		slog.Int64("zoho_total", int64(math.Round(orderDetails.GrandTotal*100))),
	).Debug("order updated")

	return nil
}

// calculateTaxRate calculates the tax rate from existing order_total data.
// Returns tax rate as a decimal (e.g., 0.23 for 23% VAT), rounded to 4 decimal places.
func (c *Core) calculateTaxRate(orderId int64) (float64, error) {
	// Get sub_total and tax from order_total table
	_, subTotal, err := c.repo.OrderTotal(orderId, "sub_total")
	if err != nil {
		return 0, fmt.Errorf("failed to get sub_total: %w", err)
	}

	_, tax, err := c.repo.OrderTotal(orderId, "tax")
	if err != nil {
		return 0, fmt.Errorf("failed to get tax: %w", err)
	}

	if subTotal == 0 {
		return 0, fmt.Errorf("sub_total is zero")
	}

	// Calculate rate and round to 4 decimals
	rate := tax / subTotal
	return math.Round(rate*10000) / 10000, nil
}

// calculateDiscountPercent calculates the discount percentage from API items.
// Compares API totals (discounted) vs full totals (price × quantity).
// Returns discount as a decimal (e.g., 0.15 for 15% discount).
func (c *Core) calculateDiscountPercent(items []entity.ApiOrderedItem) float64 {
	var sumApiTotals float64 = 0
	var sumFullTotals float64 = 0

	for _, item := range items {
		if c.shippingItemZohoId != "" && item.ZohoID == c.shippingItemZohoId {
			continue
		}
		sumApiTotals += item.Total                           // Discounted total from API
		sumFullTotals += item.Price * float64(item.Quantity) // Full price
	}

	if sumFullTotals == 0 {
		return 0
	}

	return 1.0 - (sumApiTotals / sumFullTotals)
}
