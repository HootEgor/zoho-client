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

	// b2bZohoId is a sentinel written into oc_order.zoho_id for B2B orders, which are excluded
	// from the Sales_Orders sync. It is not a real Zoho record id.
	b2bZohoId = "[B2B]"
)

type Currency struct {
	Code string
	Rate float64
}

// PushOrderToZoho fetches an order by ID from the database and pushes it to Zoho CRM. When the
// order already carries a Zoho id, the existing Sales Order is UPDATED in place rather than a
// second one created — re-pushing an order must not duplicate it or orphan its Zoho record.
// Returns the Zoho order ID on success.
func (c *Core) PushOrderToZoho(orderId int64) (string, error) {

	existingZohoId, order, err := c.repo.OrderSearchId(orderId)
	if err != nil {
		return "", fmt.Errorf("order search: %w", err)
	}

	zohoId, err := c.processOrder(order, existingZohoId, order.ClientDetails.IsB2B())
	if err != nil {
		return "", err
	}

	// An update keeps the same id; only a create needs to be written back.
	if zohoId != existingZohoId {
		if err = c.repo.ChangeOrderZohoId(orderId, zohoId); err != nil {
			return zohoId, fmt.Errorf("update zoho_id in database: %w", err)
		}
	}

	return zohoId, nil
}

// zohoOrderExists reports whether a stored zoho_id points at a real Zoho Sales Order rather than
// a sentinel the sync writes in place of one ("[B2B]" marks an order deliberately not synced).
func zohoOrderExists(zohoId string) bool {
	return zohoId != "" && zohoId != b2bZohoId
}

// processOrder handles the core order-to-Zoho flow: creates contact, validates products, builds
// the Zoho order with all items, then creates it — or, when existingZohoId already names a Zoho
// Sales Order, updates that record in place. Returns the Zoho order ID on success.
func (c *Core) processOrder(order *entity.CheckoutParams, existingZohoId string, isB2B bool) (string, error) {
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
	isUpdate := zohoOrderExists(existingZohoId)
	if !isB2B {
		zohoOrder, chunkedItems := c.buildZohoOrder(order, contactID)

		if len(chunkedItems) > 0 {
			c.log.With(
				slog.Int("exceed quantity", len(chunkedItems)),
			).Info("order contains > 200 items")

			zohoOrder.Subject += " !"
		}

		if isUpdate {
			// Re-push of an order already in Zoho: overwrite the existing Sales Order. Creating a
			// second one would duplicate the order and orphan the record the reverse webhook,
			// the payment link and zoho_modified_time all point at.
			zohoId = existingZohoId
			zohoModifiedTime, err = c.zoho.UpdateOrder(zohoOrder, existingZohoId)
			if err != nil {
				return "", fmt.Errorf("update Zoho order: %w", err)
			}
			infoTag = "order updated"
		} else {
			zohoId, zohoModifiedTime, err = c.zoho.CreateOrder(zohoOrder)
			if err != nil {
				return "", fmt.Errorf("create Zoho order: %w", err)
			}
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

	// Create payment record in Zoho if payment data is available. Only on a create: the payment
	// is a separate Zoho record linked to this order, so doing it again on a re-push would add a
	// second one for the same Stripe intent. An existing order's payment is kept in step by
	// ProcessPendingPayments / ProcessPaymentUpdates instead.
	if !isUpdate && order.PaymentStatus != "" {
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
			_ = c.repo.ChangeOrderZohoId(order.OrderId, b2bZohoId)
			continue
		}

		// GetNewOrders only returns orders with an empty zoho_id, so these are always creates.
		zohoId, err := c.processOrder(order, "", order.ClientDetails.IsB2B())
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
		if !zohoOrderExists(zohoId) {
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

// buildOrderedItem builds a Zoho subform line. discountP is the PRE-tax percentage applied
// uniformly across the order; a per-line special price (MasterPrice > Price) is combined with
// it so the line reports a single ListPrice and one effective discount.
//
// Zoho's DiscountP subform field accepts at most 2 decimal places — too coarse to pin the VAT
// base on its own. So the percentage is sent rounded to 2 decimals and the residual is folded
// into ListPrice (which takes 4 decimals): the line net Zoho recomputes as
// ListPrice x Qty x (1 - DiscountP/100) still equals the exact net. When the percentage fits
// in 2 decimals the compensation is an identity and ListPrice stays at the catalogue price.
func buildOrderedItem(lineItem *entity.LineItem, discountP float64) entity.OrderedItem {
	listPrice := lineItem.Price
	if lineItem.MasterPrice > 0 && lineItem.MasterPrice > lineItem.Price {
		listPrice = lineItem.MasterPrice
	}
	// Exact net unit = paid price further reduced by the order-level percentage.
	netUnit := lineItem.Price * (1 - discountP/100)
	lineDiscountP := 0.0
	if listPrice > 0 {
		lineDiscountP = round2((1 - netUnit/listPrice) * 100)
	}
	if lineDiscountP < 100 {
		listPrice = round4(netUnit / (1 - lineDiscountP/100))
	}
	// Net line total. Zoho recomputes this value from ListPrice and DiscountP rather than
	// storing what we send, so it is kept at full precision to match.
	totalNet := round4(lineItem.Qty * listPrice * (1 - lineDiscountP/100))
	return entity.OrderedItem{
		Product: entity.ZohoProduct{
			ID: lineItem.ZohoId,
		},
		Quantity:  int64(lineItem.Qty),
		DiscountP: lineDiscountP,
		ListPrice: listPrice,
		Total:     totalNet,
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

// taxHealthGap returns the signed difference between the VAT OpenCart declared (tax_value) and
// the VAT actually contained in the taxed portion of what the customer was charged. OpenCart
// does not tax shipping, so that portion is Total - Shipping. A positive gap means the shop
// declared VAT on money the customer never paid (VAT charged on undiscounted amounts, the
// docs/OPENCART_VAT_BUG_RU.md case); ~0 means the order is healthy.
func taxHealthGap(oc *entity.CheckoutParams) float64 {
	rate := oc.VatRate()
	if rate <= 0 {
		return 0
	}
	return oc.TaxValue - (oc.Total-oc.Shipping)*rate/(1+rate)
}

// buildZohoOrder constructs a ZohoOrder from CheckoutParams. Returns the order and any
// additional item chunks that exceed ChunkSize (100 items) for subsequent API calls.
func (c *Core) buildZohoOrder(oc *entity.CheckoutParams, contactID string) (entity.ZohoOrder, [][]*entity.OrderedItem) {
	// Every reduction OpenCart grants at the moment of sale (coupon + discount) lowers the VAT
	// base, so all of them reach Zoho as a single PRE-tax per-line discount. Nothing is left to
	// apply after tax, which is why the order-level Adjustment stays zero.
	//
	// Zoho derives its grand total as Sub_Total x (1 + VAT%), where Sub_Total is the sum of the
	// line totals it recomputes itself as ListPrice x Quantity x (1 - DiscountP/100) — the Total
	// we send is ignored. So DiscountP is the only lever on the order's net base. Pinning that
	// base at Total/(1+rate) makes Zoho's grand total equal what the customer was actually
	// charged, and the VAT Zoho records equal the VAT actually contained in that amount.
	//
	// The net base therefore needs real precision, but Zoho caps DiscountP at 2 decimal
	// places — buildOrderedItem sends the rounded percentage and folds the residual into
	// ListPrice so the recomputed base is still exact.
	rate := oc.VatRate()
	couponAmount := math.Abs(oc.Coupon)

	var discountP float64
	if oc.SubTotal > 0 {
		productNet := oc.Total/(1+rate) - oc.Shipping
		discountP = round4((1 - productNet/oc.SubTotal) * 100)
		if discountP < 0 {
			discountP = 0
		}
	}

	// Health check. Where tax_value is over-declared, the shop is still charging VAT on
	// discounted amounts and the order we are about to sync is worth more than the customer
	// paid for — see docs/OPENCART_VAT_BUG_RU.md. The order still syncs coherently (Zoho gets
	// the amount actually charged, with the VAT that amount really contains), but say so loudly.
	if gap := taxHealthGap(oc); math.Abs(gap) > 0.01 {
		c.log.With(
			slog.Int64("order_id", oc.OrderId),
			slog.Float64("tax_value", round2(oc.TaxValue)),
			slog.Float64("lawful_tax", round2(oc.TaxValue-gap)),
			slog.Float64("over_declared", math.Round(gap*100)/100),
		).Warn("OpenCart tax_value does not match the VAT contained in the taxed portion of the order total")
	}

	lineItems := oc.LineItems

	// Build all ordered items at list price, carrying the whole pre-tax reduction.
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

	return entity.ZohoOrder{
		ContactName:  entity.ContactName{ID: contactID},
		OrderedItems: orderedItems,
		// Order-level Discount/DiscountP stay zero: the whole reduction is encoded in the
		// per-line discount, and Zoho recomputes the grand total from the lines + VAT, so any
		// value here would be either ignored or double-counted.
		Discount:        0,
		DiscountP:       0,
		CouponTitle:     oc.CouponTitle,
		CouponValue:     round2(couponAmount),
		Description:     oc.Comment,
		CustomerNo:      "",
		ShippingState:   "",
		Tax:             0,
		VAT:             round0(rate * 100),
		GrandTotal:      round2(oc.Total),
		SubTotal:        round2(oc.SubTotal),
		Currency:        oc.Currency,
		BillingCountry:  oc.ClientDetails.Country,
		Carrier:         "",
		Status:          "Нове",
		SalesCommission: 0,
		DueDate:         time.Now().Format("2006-01-02"),
		BillingStreet:   oc.ClientDetails.Street,
		// Zero: every at-sale reduction lowers the VAT base and so already lives in the lines.
		// Nothing is applied after tax. (Zoho's grand total ignores this field anyway — its
		// formula is Sub_Total x (1 + VAT%).)
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
