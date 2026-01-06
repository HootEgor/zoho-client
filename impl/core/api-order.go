package core

import (
	"fmt"
	"log/slog"
	"math"
	"zohoclient/entity"
	"zohoclient/internal/database"
	"zohoclient/internal/lib/sl"
)

func (c *Core) UpdateOrder(orderDetails *entity.ApiOrder) error {
	log := c.log.With(sl.Module("core.UpdateOrder"))

	if orderDetails.ZohoID == "" {
		return fmt.Errorf("zoho_id is required")
	}

	orderId, orderParams, err := c.repo.OrderSearchByZohoId(orderDetails.ZohoID)
	if err != nil {
		return fmt.Errorf("order not found: %w", err)
	}

	currencyValue := orderParams.CurrencyValue

	log = log.With(
		slog.String("zoho_id", orderDetails.ZohoID),
		slog.Int64("order_id", orderId),
	)

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
	taxRate, err := c.calculateTaxRate(orderId)
	if err != nil {
		log.Warn("failed to calculate tax rate, using default", sl.Err(err))
		taxRate = 0.23 // Default 23% VAT
	}

	// Calculate discount percentage from API items
	discountPercent := c.calculateDiscountPercent(orderDetails.OrderedItems)

	var itemsTotal int64
	var taxTotal int64
	var shippingTotal int64

	// Prepare product data with calculated tax
	productData := make([]database.OrderProductData, 0, len(orderDetails.OrderedItems))
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
		productData = append(productData, database.OrderProductData{
			ZohoID:       item.ZohoID, // Already a string, use directly
			Quantity:     item.Quantity,
			PriceInCents: int64(math.Round(itemPrice * 100)),
			TotalInCents: int64(math.Round(lineTotal * 100)),
			TaxInCents:   int64(math.Round(taxPerUnit * 100)),
		})

		itemsTotal += int64(math.Round(lineTotal * 100))
		taxTotal += int64(math.Round(taxPerUnit*100)) * int64(item.Quantity)
	}

	// Calculate discount and final total
	discount := int64(math.Round(float64(itemsTotal+taxTotal) * discountPercent))
	total := itemsTotal + taxTotal + shippingTotal - discount

	coupon := int64(0)
	if orderDetails.Coupon != "" {
		coupon = discount
		discount = 0
	}

	// Execute entire update in a single transaction
	_ = database.OrderUpdateTransaction{
		OrderID:       orderId,
		Items:         productData,
		CurrencyValue: currencyValue,
		OrderTotal:    total,
		Totals: database.OrderTotalsData{
			SubTotal: itemsTotal,
			Tax:      taxTotal,
			Discount: discount,
			Shipping: shippingTotal,
			Total:    total,
			Coupon:   coupon,
		},
	}

	//err = c.repo.UpdateOrderWithTransaction(txData)
	//if err != nil {
	//	return fmt.Errorf("failed to update order: %w", err)
	//}

	log.With(
		slog.Int64("sub_total", itemsTotal),
		slog.Int64("shipping", shippingTotal),
		slog.Float64("discountP", discountPercent),
		slog.Int64("discount", discount),
		slog.Int64("coupon", coupon),
		slog.Int64("tax_total", taxTotal),
		slog.Float64("tax_rate", taxRate),
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
		if item.ZohoID == c.shippingItemZohoId {
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
