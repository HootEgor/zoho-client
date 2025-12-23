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
	log := c.log.With(sl.Module("core/api-order"))

	// 1. Validate input
	if orderDetails.ZohoID == "" {
		return fmt.Errorf("zoho_id is required")
	}

	// 2. Find order in database
	orderId, orderParams, err := c.repo.OrderSearchByZohoId(orderDetails.ZohoID)
	if err != nil {
		return fmt.Errorf("order not found: %w", err)
	}

	currencyValue := orderParams.CurrencyValue

	log = log.With(
		slog.String("zoho_id", orderDetails.ZohoID),
		slog.Int64("order_id", orderId),
	)

	// 3. Update status if provided
	if orderDetails.Status != "" {
		statusId := c.GetStatusIdByName(orderDetails.Status)
		if statusId > 0 {
			err = c.repo.ChangeOrderStatus(orderId, int64(statusId), "Updated via API")
			if err != nil {
				return fmt.Errorf("failed to update status: %w", err)
			}
			log.Info("status updated", slog.Int("status_id", statusId))
		}
	}

	// 4. Calculate tax rate from existing order totals
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
			ZohoID:       fmt.Sprintf("%d", item.ZohoID),
			Quantity:     item.Quantity,
			PriceInCents: int64(math.Round(item.Price * 100)),
			TotalInCents: int64(math.Round(lineTotal * 100)),
			TaxInCents:   int64(math.Round(taxPerUnit * 100)),
		})

		itemsTotal += int(math.Round(lineTotal * 100))
		taxTotal += int(math.Round(taxPerUnit * 100))
	}

	// 7. Update order items in database (simplified CRUD operation)
	orderTotal := orderDetails.GrandTotal
	if orderTotal == 0 {
		orderTotal = float64(orderParams.Total) / 100.0
	}
	err = c.repo.UpdateOrderItems(orderId, productData, currencyValue, orderTotal)
	if err != nil {
		return fmt.Errorf("failed to update order items: %w", err)
	}

	// 8. Calculate order totals
	subTotal := itemsTotal

	// 9. Get existing shipping and titles
	shippingTitle, shippingValueCents, err := c.repo.OrderTotal(orderId, "shipping", currencyValue)
	if err != nil {
		shippingTitle = "Shipping"
		shippingValueCents = 0
	}
	shipping := float64(shippingValueCents)

	taxTitle, _, _ := c.repo.OrderTotal(orderId, "tax", currencyValue)
	if taxTitle == "" {
		taxTitle = "VAT"
	}

	discountTitle, _, _ := c.repo.OrderTotal(orderId, "discount", currencyValue)
	if discountTitle == "" {
		discountTitle = "Discount"
	}

	// 10. Calculate discount and final total
	discount := float64(subTotal+taxTotal+int(shipping)) * discountPercent
	total := float64(subTotal+taxTotal+int(shipping)) - discount

	// 11. Update all entries in order_total table
	err = c.updateOrderTotals(orderId, float64(subTotal), float64(taxTotal), discount, shipping, total,
		taxTitle, discountTitle, shippingTitle, currencyValue)
	if err != nil {
		return fmt.Errorf("failed to update order totals: %w", err)
	}

	log.Info("order updated successfully",
		slog.String("zoho_id", orderDetails.ZohoID),
		slog.Int64("order_id", orderId),
		slog.Float64("total", total))

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

// updateOrderTotals updates all entries in the order_total table by calling database layer for each total type.
// Converts all values from floats to cents before passing to database layer.
func (c *Core) updateOrderTotals(orderId int64, subTotal, tax, discount, shipping, total float64,
	taxTitle, discountTitle, shippingTitle string, currencyValue float64) error {

	// Convert to cents
	subTotalCents := int64(math.Round(subTotal * currencyValue * 100))
	taxCents := int64(math.Round(tax * currencyValue * 100))
	discountCents := int64(math.Round(discount * currencyValue * 100))
	shippingCents := int64(math.Round(shipping * currencyValue * 100))
	totalCents := int64(math.Round(total * currencyValue * 100))

	// Update each total entry
	err := c.repo.UpdateOrderTotal(orderId, "sub_total", "Suma cząstkowa", subTotalCents, 1)
	if err != nil {
		return err
	}

	err = c.repo.UpdateOrderTotal(orderId, "tax", taxTitle, taxCents, 2)
	if err != nil {
		return err
	}

	err = c.repo.UpdateOrderTotal(orderId, "discount", discountTitle, discountCents, 3)
	if err != nil {
		return err
	}

	err = c.repo.UpdateOrderTotal(orderId, "shipping", shippingTitle, shippingCents, 4)
	if err != nil {
		return err
	}

	err = c.repo.UpdateOrderTotal(orderId, "total", "Razem", totalCents, 5)
	if err != nil {
		return err
	}

	return nil
}
