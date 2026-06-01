package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
	"zohoclient/internal/services"
)

const (
	ZohoLocation    = "Польша"
	ZohoOrderSource = "OpenCart"

	ChunkSize = 200

	// paymentZohoIdError is a sentinel written into oc_order.zoho_payment_id when a
	// payment cannot be created in Zoho due to non-transient errors (e.g. the linked
	// Sales Order was deleted), so the order is not retried forever.
	paymentZohoIdError = "[ERR]"
)

type Currency struct {
	Code string
	Rate float64
}

// PushOrderToZoho fetches an order by ID from the database and pushes it to Zoho CRM.
// Returns the Zoho order ID on success.
func (c *Core) PushOrderToZoho(orderId int64) (string, error) {

	_, order, err := c.repo.OrderSearchId(orderId)
	if err != nil {
		return "", fmt.Errorf("order search: %w", err)
	}

	zohoId, err := c.processOrder(order, order.ClientDetails.IsB2B())
	if err != nil {
		return "", err
	}

	err = c.repo.ChangeOrderZohoId(orderId, zohoId)
	if err != nil {
		return zohoId, fmt.Errorf("update zoho_id in database: %w", err)
	}

	return zohoId, nil
}

// processOrder handles the core order-to-Zoho flow: creates contact, validates products,
// builds and creates the Zoho order with all items. Returns the Zoho order ID on success.
func (c *Core) processOrder(order *entity.CheckoutParams, isB2B bool) (string, error) {
	log := c.log.With(
		slog.Int64("order_id", order.OrderId),
		slog.String("currency", order.Currency),
		slog.String("tax", order.TaxTitle),
		slog.Float64("total", round2(order.Total)),
		slog.Float64("tax_value", round2(order.TaxValue)),
		slog.String("coupon", order.CouponTitle),
		slog.Float64("shipping", order.Shipping),
		slog.String("name", fmt.Sprintf("%s : %s", order.ClientDetails.FirstName, order.ClientDetails.LastName)),
		slog.String("country", order.ClientDetails.Country),
		slog.String("tax_id", order.ClientDetails.TaxId),
	)

	if err := order.Validate(); err != nil {
		log.With(sl.Err(err)).Warn("validation failed")
		return "", err
	}

	// Create or find contact in Zoho
	contactID, err := c.zoho.CreateContact(order.ClientDetails)
	if err != nil {
		log.With(
			slog.String("email", order.ClientDetails.Email),
			slog.String("phone", order.ClientDetails.Phone),
			sl.Err(err),
		).Error("create contact")
		return "", fmt.Errorf("create contact: %w", err)
	}

	// Validate product UIDs
	if err := hasEmptyUid(order.LineItems); err != nil {
		return "", fmt.Errorf("product without UID: %w", err)
	}

	// Fetch missing Zoho IDs for products
	if err := hasEmptyZohoID(order.LineItems); err != nil {
		c.processProductsWithoutZohoID(order.LineItems)

		if err := hasEmptyZohoID(order.LineItems); err != nil {
			return "", fmt.Errorf("product without Zoho ID: %w", err)
		}
	}

	// Build and create Zoho order

	zohoId := ""
	zohoModifiedTime := ""
	infoTag := "order created"
	if !isB2B {
		zohoOrder, chunkedItems := c.buildZohoOrder(order, contactID)

		if len(chunkedItems) > 0 {
			c.log.With(
				slog.Int("exceed quantity", len(chunkedItems)),
			).Info("order contains > 200 items")

			zohoOrder.Subject += " !"
		}

		zohoId, zohoModifiedTime, err = c.zoho.CreateOrder(zohoOrder)
		if err != nil {
			return "", fmt.Errorf("create Zoho order: %w", err)
		}

		//// Add remaining items in chunks
		//if err := addChunkedItems(chunkedItems, func(chunk []*entity.OrderedItem) (string, error) {
		//	return c.zoho.AddItemsToOrder(zohoId, chunk)
		//}); err != nil {
		//	log.With(sl.Err(err)).Error("add items to order")
		//	return "", err
		//}
	} else {
		//zohoOrder, chunkedItems := c.buildZohoOrderB2B(order, contactID)
		//zohoId, err = c.createB2BDealWithItems(zohoOrder, chunkedItems)
		//if err != nil {
		//	log.With(sl.Err(err)).Error("create B2B deal")
		//	return "", err
		//}
		//infoTag = "B2B order created"
	}

	// Create payment record in Zoho if payment data is available
	if order.PaymentStatus != "" {
		c.createZohoPayment(order, zohoId)
	}

	// Store the Modified_Time returned by Zoho so the echo webhook for this create
	// (whose Modified_Time will be <= this value) is suppressed by UpdateOrder.
	if t, ok := parseZohoTime(zohoModifiedTime); ok {
		if err := c.repo.SetOrderZohoModifiedTime(order.OrderId, t); err != nil {
			log.With(sl.Err(err)).Warn("store zoho_modified_time failed")
		}
	} else if zohoModifiedTime != "" {
		log.With(slog.String("zoho_modified_time", zohoModifiedTime)).
			Warn("could not parse Zoho Modified_Time")
	}

	// Save order version to MongoDB
	c.saveOrderVersionToMongo(order.OrderId, order)

	log.With(slog.String("zoho_id", zohoId)).Info(infoTag)

	return zohoId, nil
}

// ProcessOrders fetches all new orders from the database and pushes them to Zoho CRM.
// B2B orders are skipped and marked with "[B2B]" zoho_id. Orders with missing product
// UIDs or Zoho IDs are skipped until the missing data is available.
func (c *Core) ProcessOrders() {
	orders, err := c.repo.GetNewOrders()
	if err != nil {
		c.log.With(sl.Err(err)).Error("failed to get new orders")
		return
	}

	for _, order := range orders {
		log := c.log.With(
			slog.Int64("order_id", order.OrderId),
		)

		if order.ClientDetails.IsB2B() {
			log.With(slog.Int64("group_id", order.ClientDetails.GroupId)).Debug("b2b client")
			_ = c.repo.ChangeOrderZohoId(order.OrderId, "[B2B]")
			continue
		}

		zohoId, err := c.processOrder(order, order.ClientDetails.IsB2B())
		if err != nil {
			log.With(sl.Err(err)).Error("process order failed")
			continue
		}

		err = c.repo.ChangeOrderZohoId(order.OrderId, zohoId)
		if err != nil {
			log.With(sl.Err(err)).Error("update order zoho_id")
		}
	}
}

// createZohoPayment builds a ZohoPayment from order data and creates it in Zoho CRM,
// linked to the given Sales Order via the Sells lookup field.
func (c *Core) createZohoPayment(order *entity.CheckoutParams, zohoOrderId string) {
	log := c.log.With(
		slog.Int64("order_id", order.OrderId),
		slog.String("zoho_order_id", zohoOrderId),
		slog.String("payment_status", order.PaymentStatus),
	)

	payment := entity.ZohoPayment{
		Name:                    fmt.Sprintf("Payment #%d", order.OrderId),
		Sells:                   entity.ZohoSellsRef{ID: zohoOrderId},
		Sum:                     round2(float64(order.PaymentAmount) / 100),
		Currency:                order.Currency,
		StripePaymentIntentID:   order.PaymentId,
		StripeCheckoutSessionID: order.PaymentSessionId,
		PaymentTime:             time.Now().Format("2006-01-02T15:04:05+02:00"),
	}

	payment.Status = entity.ConvertPaymentStatus(order.PaymentStatus)

	if order.ClientDetails != nil {
		payment.Email = order.ClientDetails.Email
	}

	zohoPaymentId, err := c.zoho.CreatePayment(payment)
	if err != nil {
		log.With(sl.Err(err)).Error("create Zoho payment")
		// Non-transient failure (e.g. linked Sales Order deleted in Zoho):
		// mark with a sentinel so the order is not retried forever.
		if errors.Is(err, services.ErrPaymentInvalidData) {
			if markErr := c.repo.UpdateOrderZohoPaymentId(order.OrderId, paymentZohoIdError); markErr != nil {
				log.With(sl.Err(markErr)).Error("mark failed payment")
			}
		}
		return
	}

	// Record both the created payment id and the wf_payment_status it reflects, so a later
	// status change (e.g. held -> paid) is detected by ProcessPaymentUpdates.
	err = c.repo.UpdateOrderZohoPayment(order.OrderId, zohoPaymentId, order.PaymentStatus)
	if err != nil {
		log.With(sl.Err(err)).Error("update zoho_payment")
		return
	}

	log.With(slog.String("zoho_payment_id", zohoPaymentId)).Info("payment created")
}

// ProcessPaymentUpdates finds orders whose Zoho payment record already exists but whose
// wfsync payment status has since advanced (e.g. a confirmed hold that was later captured),
// and pushes the new status to the linked Zoho Payments record.
func (c *Core) ProcessPaymentUpdates() {
	orders, err := c.repo.GetOrdersPendingPaymentUpdate()
	if err != nil {
		c.log.With(sl.Err(err)).Error("get orders pending payment update")
		return
	}

	for _, order := range orders {
		c.updateZohoPayment(order)
	}
}

// updateZohoPayment pushes the current wf_payment_status of an order to its existing
// Zoho Payments record and records the synced status on success.
func (c *Core) updateZohoPayment(order *entity.CheckoutParams) {
	log := c.log.With(
		slog.Int64("order_id", order.OrderId),
		slog.String("payment_status", order.PaymentStatus),
	)

	zohoPaymentId, err := c.repo.GetOrderZohoPaymentId(order.OrderId)
	if err != nil {
		log.With(sl.Err(err)).Error("get zoho_payment_id for update")
		return
	}
	// Defensive: the query already excludes empty / "[ERR]" ids.
	if zohoPaymentId == "" || zohoPaymentId == paymentZohoIdError {
		return
	}

	zohoStatus := entity.ConvertPaymentStatus(order.PaymentStatus)
	if err := c.zoho.UpdatePaymentStatus(zohoPaymentId, zohoStatus); err != nil {
		log.With(sl.Err(err)).Error("update Zoho payment status")
		return
	}

	if err := c.repo.SetOrderZohoPaymentStatus(order.OrderId, order.PaymentStatus); err != nil {
		log.With(sl.Err(err)).Error("store synced zoho_payment_status")
		return
	}

	log.With(
		slog.String("zoho_payment_id", zohoPaymentId),
		slog.String("zoho_status", zohoStatus),
	).Info("payment status updated")
}

// ProcessPendingPayments finds orders already synced to Zoho that have received payment
// data from wfsync but don't have a Zoho payment record yet, and creates the payment records.
func (c *Core) ProcessPendingPayments() {
	orders, err := c.repo.GetOrdersPendingPayment()
	if err != nil {
		c.log.With(sl.Err(err)).Error("get orders pending payment")
		return
	}

	for _, order := range orders {
		zohoId, err := c.repo.GetOrderZohoId(order.OrderId)
		if err != nil {
			c.log.With(
				sl.Err(err),
				slog.Int64("order_id", order.OrderId),
			).Error("get zoho_id for payment update")
			continue
		}
		//records are selected from database by non-empty zohoId;
		//"[B2B]" is a sentinel marking orders with no real Sales Order to link a payment to
		if zohoId == "" || zohoId == "[B2B]" {
			continue
		}

		c.createZohoPayment(order, zohoId)
	}
}

// hasEmptyZohoID checks if any product in the slice has an empty ZohoId.
// Returns an error with product details if found, nil otherwise.
func hasEmptyZohoID(products []*entity.LineItem) error {
	for _, p := range products {
		if p.ZohoId == "" {
			return fmt.Errorf("product id=%d %s has empty zoho_id", p.Id, p.Name)
		}
	}
	return nil
}

// hasEmptyUid checks if any product in the slice has an empty UID.
// Returns an error with product details if found, nil otherwise.
func hasEmptyUid(products []*entity.LineItem) error {
	for _, p := range products {
		if p.Uid == "" {
			return fmt.Errorf("product id=%d %s has empty UID", p.Id, p.Name)
		}
	}
	return nil
}

// processProductsWithoutZohoID fetches Zoho IDs from the product repository for products
// that don't have them. Updates both the in-memory slice and the database.
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
				products[i].ZohoId = zohoID
			}
		}
	}
}

// buildOrderedItem converts a LineItem to a Zoho OrderedItem with the given discount percentage.
// When the LineItem carries a MasterPrice greater than the paid Price, the line is treated as
// having a per-line "special price" discount: ListPrice is set to MasterPrice and DiscountP is
// derived from the price gap, overriding the order-level discount for that line.
func buildOrderedItem(lineItem *entity.LineItem, discountP float64) entity.OrderedItem {
	listPrice := lineItem.Price
	lineDiscountP := discountP
	if lineItem.MasterPrice > 0 && lineItem.MasterPrice > lineItem.Price {
		listPrice = lineItem.MasterPrice
		lineDiscountP = round2((1 - lineItem.Price/lineItem.MasterPrice) * 100)
	}
	totalWithDiscount := round2(lineItem.Qty * lineItem.Price * lineDiscountP / 100)
	return entity.OrderedItem{
		Product: entity.ZohoProduct{
			ID: lineItem.ZohoId,
		},
		Quantity: int64(lineItem.Qty),
		//Discount:
		DiscountP: lineDiscountP,
		ListPrice: listPrice,
		Total:     totalWithDiscount,
	}
}

func buildGood(lineItem *entity.LineItem, currency Currency, discountP float64) entity.Good {
	totalWithDiscount := round2(lineItem.Qty * lineItem.Price * discountP / 100)
	good := entity.Good{
		Product: entity.ZohoProduct{
			ID: lineItem.ZohoId,
		},
		Name:     lineItem.Name,
		Quantity: int64(lineItem.Qty),
		//Discount:
		DiscountP: discountP,
	}

	switch currency.Code {
	case entity.CurrencyUAH:
		good.PriceUAH = round2(lineItem.Price * currency.Rate)
		good.TotalUAH = round2(totalWithDiscount * currency.Rate)
		break
	case entity.CurrencyPLN:
		good.PricePLN = round2(lineItem.Price)
		good.TotalPLN = round2(totalWithDiscount)
		break
	case entity.CurrencyUSD:
		good.PriceUSD = round2(lineItem.Price * currency.Rate)
		good.TotalUSD = round2(totalWithDiscount * currency.Rate)
		break
	case entity.CurrencyEUR:
		good.PriceEUR = round2(lineItem.Price * currency.Rate)
		good.TotalEUR = round2(totalWithDiscount * currency.Rate)
		break
	}

	return good
}

// buildZohoOrder constructs a ZohoOrder from CheckoutParams. Returns the order and any
// additional item chunks that exceed ChunkSize (100 items) for subsequent API calls.
func (c *Core) buildZohoOrder(oc *entity.CheckoutParams, contactID string) (entity.ZohoOrder, [][]*entity.OrderedItem) {
	discount, discountP := oc.GetDiscount()
	discountP = round0(discountP)

	lineItems := oc.LineItems

	// Build all ordered items
	allItems := make([]entity.OrderedItem, 0, len(lineItems))
	for _, d := range lineItems {
		allItems = append(allItems, buildOrderedItem(d, discountP))
	}
	// Add shipping as item without discount
	if oc.Shipping > 0 {
		shippingItem := &entity.LineItem{
			ZohoId: c.shippingItemZohoId,
			Qty:    1,
			Price:  oc.Shipping,
		}
		allItems = append(allItems, buildOrderedItem(shippingItem, 0))
	}

	// Split into main order items (first chunk) and remaining chunks
	var orderedItems []entity.OrderedItem
	var chunkedItems [][]*entity.OrderedItem

	if len(allItems) <= ChunkSize {
		orderedItems = allItems
	} else {
		orderedItems = allItems[:ChunkSize]
		remaining := allItems[ChunkSize:]
		chunkedItems = chunkSlice(remaining, ChunkSize)
	}

	// if an order has coupon set, move discount percent to promocode
	couponTitle := ""
	couponValue := 0.0
	if oc.CouponTitle != "" {
		couponTitle = oc.CouponTitle
		couponValue = discount
		discount = 0
		discountP = 0.0
	}

	return entity.ZohoOrder{
		ContactName:        entity.ContactName{ID: contactID},
		OrderedItems:       orderedItems,
		Discount:           round2(discount),
		DiscountP:          round0(discountP),
		CouponTitle:        couponTitle,
		CouponValue:        round2(couponValue),
		Description:        oc.Comment,
		CustomerNo:         "",
		ShippingState:      "",
		Tax:                0,
		VAT:                round0(oc.TaxRate()),
		GrandTotal:         round2(oc.Total),
		SubTotal:           round2(oc.Total - oc.TaxValue),
		Currency:           oc.Currency,
		BillingCountry:     oc.ClientDetails.Country,
		Carrier:            "",
		Status:             "Нове",
		SalesCommission:    0,
		DueDate:            time.Now().Format("2006-01-02"),
		BillingStreet:      oc.ClientDetails.Street,
		Adjustment:         0,
		TermsAndConditions: "Standard terms apply.",
		BillingCode:        oc.ClientDetails.ZipCode,
		ProductDetails:     nil,
		Subject:            fmt.Sprintf("Order #%d", oc.OrderId),
		IDsite:             fmt.Sprintf("%d", oc.OrderId),
		NIP:                oc.ClientDetails.TaxId,
		Location:           ZohoLocation,
		OrderSource:        ZohoOrderSource,
		Postcode:           oc.ClientDetails.ZipCode,
		RecipientCountry:   oc.ClientDetails.Country,
		RecipientRegion:    oc.ClientDetails.Region,
		RecipientCity:      oc.ClientDetails.City,
		RecipientAddress:   oc.ClientDetails.Street,
		RecipientCityId:    recipientCityId(oc.ClientDetails),
		PostTerminal:       oc.PostTerminal,
		PostType:           mapPostType(oc.ShippingCode),
	}, chunkedItems
}

// mapPostType maps an OpenCart shipping_code to the corresponding Zoho Post_type value.
// Returns an empty string when the code is unknown, so omitempty drops the field.
func mapPostType(shippingCode string) string {
	switch shippingCode {
	case "filterit1.filterit0":
		return "InPost (кур'єр)"
	case "filterit1.filterit1":
		return "InPost (поштомат)"
	case "filterit2.filterit0":
		return "DHL (кур'єр)"
	case "pickup.pickup":
		return "Самовивіз"
	default:
		return ""
	}
}

func recipientCityId(client *entity.ClientDetails) string {
	if client.CityId != 0 {
		return fmt.Sprintf("%d", client.CityId)
	}
	return client.ZipCode
}

func (c *Core) buildZohoOrderB2B(oc *entity.CheckoutParams, contactID string) (entity.ZohoOrderB2B, [][]*entity.Good) {
	_, discountP := oc.GetDiscount()
	discountP = round0(discountP)

	lineItems := oc.LineItems

	orderCurrency := Currency{
		Code: oc.Currency,
		Rate: oc.CurrencyValue,
	}

	// Build all ordered items
	allItems := make([]entity.Good, 0, len(lineItems))
	for _, d := range lineItems {
		allItems = append(allItems, buildGood(d, orderCurrency, discountP))
	}
	// Add shipping as item without discount
	if oc.Shipping > 0 {
		shippingItem := &entity.LineItem{
			ZohoId: c.shippingItemZohoId,
			Qty:    1,
			Price:  oc.Shipping,
		}
		allItems = append(allItems, buildGood(shippingItem, orderCurrency, 0))
	}

	// Split into chunks
	chunkedItems := chunkSlice(allItems, ChunkSize)

	order := entity.ZohoOrderB2B{
		ContactName: entity.ContactName{ID: contactID},
		//Goods:       orderedItems,
		//Discount:           round2(discount),
		DiscountP:   round0(discountP),
		Description: oc.Comment,
		//CustomerNo:         "",
		VAT:            round0(oc.TaxRate()),
		Currency:       oc.Currency,
		BillingCountry: oc.ClientDetails.Country,
		Status:         c.statusesB2B[oc.StatusId],
		Pipeline:       "B2B",
		BillingStreet:  oc.ClientDetails.Street,
		Subject:        fmt.Sprintf("Order #%d", oc.OrderId),
		NIP:            oc.ClientDetails.TaxId,
		Location:       ZohoLocation,
		OrderSource:    ZohoOrderSource,
	}

	setCurrencyTotals(&order, orderCurrency.Code,
		oc.Total*orderCurrency.Rate,
		(oc.Total-oc.TaxValue)*orderCurrency.Rate,
	)

	return order, chunkedItems
}

// createB2BDealWithItems creates a B2B deal in Zoho and adds all items.
// Handles the full flow: create deal, fill deal ID into items, add items in chunks.
func (c *Core) createB2BDealWithItems(order entity.ZohoOrderB2B, chunkedItems [][]*entity.Good) (string, error) {
	// Create deal
	zohoId, err := c.zoho.CreateB2BOrder(order)
	if err != nil {
		return "", fmt.Errorf("create Zoho deal: %w", err)
	}

	// Fill deal ID into all goods items
	for _, chunk := range chunkedItems {
		for _, item := range chunk {
			item.Deal = entity.ZohoDeal{ID: zohoId}
		}
	}

	// Add items in chunks
	if err := addChunkedItems(chunkedItems, func(chunk []*entity.Good) (string, error) {
		return c.zoho.AddItemsToOrderB2B(zohoId, chunk)
	}); err != nil {
		return zohoId, err
	}

	return zohoId, nil
}

// addChunkedItems iterates over chunks and calls the provided addFunc for each.
// Returns an error if any chunk fails to be added.
func addChunkedItems[T any](chunks [][]*T, addFunc func([]*T) (string, error)) error {
	for i, chunk := range chunks {
		_, err := addFunc(chunk)
		if err != nil {
			return fmt.Errorf("add items to order (chunk %d): %w", i+1, err)
		}
	}
	return nil
}

// chunkSlice splits a slice into chunks of the specified size, returning pointers to elements.
func chunkSlice[T any](items []T, size int) [][]*T {
	var chunks [][]*T
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		chunk := make([]*T, end-i)
		for j := i; j < end; j++ {
			chunk[j-i] = &items[j]
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

// setCurrencyTotals assigns grand total and sub total to the correct currency-specific
// fields on a ZohoOrderB2B, based on the currency code.
func setCurrencyTotals(order *entity.ZohoOrderB2B, code string, grandTotal, subTotal float64) {
	switch code {
	case entity.CurrencyUAH:
		order.GrandTotalUAH = round2(grandTotal)
		order.SubTotalUAH = round2(subTotal)
	case entity.CurrencyPLN:
		order.GrandTotalPLN = round2(grandTotal)
		order.SubTotalPLN = round2(subTotal)
	case entity.CurrencyUSD:
		order.GrandTotalUSD = round2(grandTotal)
		order.SubTotalUSD = round2(subTotal)
	case entity.CurrencyEUR:
		order.GrandTotalEUR = round2(grandTotal)
		order.SubTotalEUR = round2(subTotal)
	}
}

// round0 rounds a float64 to the nearest integer, converting negative values to positive.
func round0(value float64) float64 {
	if value < 0 {
		value = -value
	}
	return math.Round(value)
}

// round2 rounds a float64 to 2 decimal points
func round2(value float64) float64 {
	if value < 0 {
		value = -value
	}
	return math.Round(value*100) / 100
}

// fmtCents formats an int64 cents amount as a 2-decimal currency string.
func fmtCents(cents int64) string {
	return fmt.Sprintf("%.2f", float64(cents)/100)
}

// round4 rounds a float64 to 4 decimal points, used for rates and percentages.
func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}

// saveOrderVersionToMongo saves the order payload as a new version to MongoDB.
func (c *Core) saveOrderVersionToMongo(orderID int64, payload interface{}) {
	if c.mongoRepo == nil {
		return
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		c.log.With(sl.Err(err), slog.Int64("order_id", orderID)).Warn("failed to marshal order payload for mongo")
		return
	}

	err = c.mongoRepo.SaveOrderVersion(orderID, string(payloadBytes))
	if err != nil {
		c.log.With(sl.Err(err), slog.Int64("order_id", orderID)).Warn("failed to save order version to mongo")
	}
}
