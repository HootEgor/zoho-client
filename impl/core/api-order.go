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
			err = c.repo.ChangeOrderStatus(orderId, int64(statusId), "Updated via API")
			if err != nil {
				return fmt.Errorf("failed to update status: %w", err)
			}
		}
	}

	// Calculate tax rate from existing order totals
	taxRate, err := c.calculateTaxRate(orderId, currencyValue)
	if err != nil {
		log.Warn("failed to calculate tax rate, using default", sl.Err(err))
		taxRate = 0.23 // Default 23% VAT
	}

	// 5. Calculate discount percentage from API items
	discountPercent := c.calculateDiscountPercent(orderDetails.OrderedItems)

	itemsTotal := 0
	taxTotal := 0
	// 6. Prepare product data with calculated tax
	productData := make([]database.OrderProductData, 0, len(orderDetails.OrderedItems))
	for _, item := range orderDetails.OrderedItems {
		// Calculate tax per unit
		taxPerUnit := item.Price * taxRate

		// Calculate line total (price × quantity, no discount)
		lineTotal := item.Price * float64(item.Quantity)

		// Convert to cents
		productData = append(productData, database.OrderProductData{
			ZohoID:       item.ZohoID, // Already a string, use directly
			Quantity:     item.Quantity,
			PriceInCents: int64(math.Round(item.Price * 100)),
			TotalInCents: int64(math.Round(lineTotal * 100)),
			TaxInCents:   int64(math.Round(taxPerUnit * 100)),
		})

		itemsTotal += int(math.Round(lineTotal * 100))
		taxTotal += int(math.Round(taxPerUnit*100)) * item.Quantity
	}

	// 7. Get existing shipping and titles (before transaction)
	shippingTitle, shippingValueCents, err := c.repo.OrderTotal(orderId, "shipping", currencyValue)
	if err != nil {
		shippingTitle = "Shipping"
		shippingValueCents = 0
	}
	shipping := shippingValueCents

	taxTitle, _, _ := c.repo.OrderTotal(orderId, "tax", currencyValue)
	if taxTitle == "" {
		taxTitle = "VAT"
	}

	discountTitle, _, _ := c.repo.OrderTotal(orderId, "discount", currencyValue)
	if discountTitle == "" {
		discountTitle = "Discount"
	}

	//taxTotal -= int(shipping)
	// 8. Calculate discount and final total
	discount := int64(math.Round(float64(itemsTotal+taxTotal+int(shipping)) * discountPercent))
	total := int64(itemsTotal + taxTotal + int(shipping) - int(discount))

	// 9. Determine order total for database
	orderTotal := orderDetails.GrandTotal
	if orderTotal == 0 {
		orderTotal = float64(orderParams.Total) / 100.0
	}

	// 10. Execute entire update in a single transaction
	txData := database.OrderUpdateTransaction{
		OrderID:       orderId,
		Items:         productData,
		CurrencyValue: currencyValue,
		OrderTotal:    orderTotal,
		Totals: database.OrderTotalsData{
			SubTotal:      int64(itemsTotal),
			Tax:           int64(taxTotal),
			TaxTitle:      taxTitle,
			Discount:      discount,
			DiscountTitle: discountTitle,
			Shipping:      shipping,
			ShippingTitle: shippingTitle,
			Total:         total,
		},
	}

	err = c.repo.UpdateOrderWithTransaction(txData)
	if err != nil {
		return fmt.Errorf("failed to update order: %w", err)
	}

	log.With(
		slog.Int64("sub_total", int64(itemsTotal)),
		slog.Int64("shipping", shipping),
		slog.Int64("discount", discount),
		slog.Int("tax_total", taxTotal),
		slog.Float64("tax_rate", taxRate),
		slog.Int64("total", total),
	).Debug("order updated")

	return nil
}

// calculateTaxRate calculates the tax rate from existing order_total data.
// Returns tax rate as a decimal (e.g., 0.23 for 23% VAT), rounded to 4 decimal places.
func (c *Core) calculateTaxRate(orderId int64, currencyValue float64) (float64, error) {
	// Get sub_total and tax from order_total table
	_, subTotalCents, err := c.repo.OrderTotal(orderId, "sub_total", currencyValue)
	if err != nil {
		return 0, fmt.Errorf("failed to get sub_total: %w", err)
	}

	_, taxCents, err := c.repo.OrderTotal(orderId, "tax", currencyValue)
	if err != nil {
		return 0, fmt.Errorf("failed to get tax: %w", err)
	}

	if subTotalCents == 0 {
		return 0, fmt.Errorf("sub_total is zero")
	}

	// Calculate rate and round to 4 decimals
	rate := float64(taxCents) / float64(subTotalCents)
	return math.Round(rate*10000) / 10000, nil
}

// calculateDiscountPercent calculates the discount percentage from API items.
// Compares API totals (discounted) vs full totals (price × quantity).
// Returns discount as a decimal (e.g., 0.15 for 15% discount).
func (c *Core) calculateDiscountPercent(items []entity.ApiOrderedItem) float64 {
	var sumApiTotals float64 = 0
	var sumFullTotals float64 = 0

	for _, item := range items {
		sumApiTotals += item.Total                           // Discounted total from API
		sumFullTotals += item.Price * float64(item.Quantity) // Full price
	}

	if sumFullTotals == 0 {
		return 0
	}

	return 1.0 - (sumApiTotals / sumFullTotals)
}
