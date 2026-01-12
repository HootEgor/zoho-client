package core

import (
	"fmt"
	"log/slog"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

const (
	B2BWebhookPipeline    = "B2B"
	B2BWebhookOrderSource = "B2B Portal"
	B2BWebhookStatus      = "Нове замовлення"
)

// ProcessB2BWebhook handles incoming B2B webhook and creates a Zoho Deal
func (c *Core) ProcessB2BWebhook(payload *entity.B2BWebhookPayload) (string, error) {
	log := c.log.With(
		slog.String("order_uid", payload.Data.OrderUID),
		slog.String("order_number", payload.Data.OrderNumber),
		slog.String("currency", payload.Data.CurrencyCode),
		slog.Float64("total", payload.Data.Total),
	)

	// Step 1: Resolve Zoho product IDs for all items
	lineItems, err := c.resolveB2BWebhookProducts(payload.Data.Items)
	if err != nil {
		log.With(sl.Err(err)).Error("failed to resolve product Zoho IDs")
		return "", fmt.Errorf("resolve product Zoho IDs: %w", err)
	}

	// Step 2: Create/find contact (placeholder with client_uid for now)
	contactID, err := c.resolveB2BWebhookContact(&payload.Data)
	if err != nil {
		log.With(sl.Err(err)).Error("failed to resolve contact")
		return "", fmt.Errorf("resolve contact: %w", err)
	}

	// Step 3: Build Zoho B2B order
	zohoOrder, chunkedItems := c.buildZohoOrderFromWebhook(&payload.Data, contactID, lineItems)

	// Step 4: Create Deal in Zoho
	zohoId, err := c.zoho.CreateB2BOrder(zohoOrder)
	if err != nil {
		log.With(sl.Err(err)).Error("failed to create Zoho Deal")
		return "", fmt.Errorf("create Zoho Deal: %w", err)
	}

	// Step 5: Add items in chunks
	for i, chunk := range chunkedItems {
		_, err = c.zoho.AddItemsToOrderB2B(zohoId, chunk)
		if err != nil {
			log.With(
				sl.Err(err),
				slog.Int("chunk", i+1),
			).Error("add items to Deal")
			return "", fmt.Errorf("add items to Deal (chunk %d): %w", i+1, err)
		}
	}

	log.With(slog.String("zoho_id", zohoId)).Info("B2B Deal created from webhook")
	return zohoId, nil
}

// resolveB2BWebhookProducts fetches Zoho IDs for all products in the webhook
func (c *Core) resolveB2BWebhookProducts(items []entity.B2BWebhookItem) ([]*entity.LineItem, error) {
	lineItems := make([]*entity.LineItem, 0, len(items))

	for _, item := range items {
		if item.ProductUID == "" {
			return nil, fmt.Errorf("product has empty UID (SKU: %s)", item.ProductSKU)
		}

		// Fetch Zoho ID from product repository
		zohoID, err := c.prodRepo.GetProductZohoID(item.ProductUID)
		if err != nil {
			return nil, fmt.Errorf("get Zoho ID for product %s: %w", item.ProductUID, err)
		}

		if zohoID == "" {
			return nil, fmt.Errorf("product %s has no Zoho ID", item.ProductUID)
		}

		lineItems = append(lineItems, &entity.LineItem{
			Uid:    item.ProductUID,
			ZohoId: zohoID,
			Qty:    float64(item.Quantity),
			Price:  item.Price,
			Tax:    item.Tax,
			Total:  item.Total,
			Sku:    item.ProductSKU,
		})
	}

	return lineItems, nil
}

// resolveB2BWebhookContact creates or finds a contact for the B2B order.
// Uses placeholder fields until client data is added to webhook payload.
func (c *Core) resolveB2BWebhookContact(order *entity.B2BWebhookOrder) (string, error) {
	clientDetails := &entity.ClientDetails{
		FirstName: order.ClientName,
		LastName:  "",
		Email:     order.ClientEmail,
		Phone:     order.ClientPhone,
		Country:   order.ClientCountry,
		City:      order.ClientCity,
		Street:    order.ClientStreet,
		ZipCode:   order.ClientZipCode,
		TaxId:     order.ClientTaxID,
	}

	// Use placeholder if client name is empty
	if clientDetails.FirstName == "" {
		clientDetails.FirstName = "B2B Client"
		clientDetails.LastName = order.ClientUID
	}

	// If no email and no phone, use placeholder email
	if clientDetails.Email == "" && clientDetails.Phone == "" {
		clientDetails.Email = fmt.Sprintf("%s@b2b.placeholder.local", order.ClientUID)
	}

	contactID, err := c.zoho.CreateContact(clientDetails)
	if err != nil {
		return "", fmt.Errorf("create contact: %w", err)
	}

	return contactID, nil
}

// buildZohoOrderFromWebhook converts webhook data to ZohoOrderB2B
func (c *Core) buildZohoOrderFromWebhook(
	order *entity.B2BWebhookOrder,
	contactID string,
	lineItems []*entity.LineItem,
) (entity.ZohoOrderB2B, [][]*entity.Good) {

	discountP := round0(order.DiscountPercent)

	orderCurrency := Currency{
		Code: order.CurrencyCode,
		Rate: 1.0, // B2B portal sends values in target currency
	}

	// Build all goods items
	allItems := make([]entity.Good, 0, len(lineItems))
	for _, item := range lineItems {
		allItems = append(allItems, buildGood(item, orderCurrency, discountP))
	}

	// Chunk items for Zoho API (max 100 per call)
	var chunkedItems [][]*entity.Good
	for i := 0; i < len(allItems); i += ChunkSize {
		end := i + ChunkSize
		if end > len(allItems) {
			end = len(allItems)
		}
		chunk := make([]*entity.Good, end-i)
		for j := i; j < end; j++ {
			chunk[j-i] = &allItems[j]
		}
		chunkedItems = append(chunkedItems, chunk)
	}

	// Calculate VAT rate from totals
	vatRate := 0.0
	if order.Subtotal > 0 && order.TotalVAT > 0 {
		vatRate = round0(order.TotalVAT * 100 / order.Subtotal)
	}

	zohoOrder := entity.ZohoOrderB2B{
		ContactName:    entity.ContactName{ID: contactID},
		DiscountP:      discountP,
		Description:    order.Comment,
		VAT:            vatRate,
		Currency:       order.CurrencyCode,
		BillingCountry: order.ClientCountry,
		Status:         B2BWebhookStatus,
		Pipeline:       B2BWebhookPipeline,
		BillingStreet:  order.ShippingAddress,
		Subject:        fmt.Sprintf("B2B Order %s", order.OrderNumber),
		Location:       ZohoLocation,
		OrderSource:    B2BWebhookOrderSource,
	}

	// Set currency-specific totals
	switch orderCurrency.Code {
	case entity.CurrencyUAH:
		zohoOrder.GrandTotalUAH = round2(order.Total)
		zohoOrder.SubTotalUAH = round2(order.Subtotal)
	case entity.CurrencyPLN:
		zohoOrder.GrandTotalPLN = round2(order.Total)
		zohoOrder.SubTotalPLN = round2(order.Subtotal)
	case entity.CurrencyUSD:
		zohoOrder.GrandTotalUSD = round2(order.Total)
		zohoOrder.SubTotalUSD = round2(order.Subtotal)
	case entity.CurrencyEUR:
		zohoOrder.GrandTotalEUR = round2(order.Total)
		zohoOrder.SubTotalEUR = round2(order.Subtotal)
	}

	return zohoOrder, chunkedItems
}
