package config

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"zohoclient/entity"
)

// Logical post-type keys. A shop's OpenCart shipping module codes map onto these
// (site.shipping_code_map), and these map onto the Zoho Post_type picklist (zoho.post_types).
// The indirection exists because both halves differ per shop while the keyword rules that read
// a shipping method name ("InPost Paczkomat", "DHL Kurier", …) are generic.
const (
	PostKeyInPost         = "inpost"
	PostKeyInPostCourier  = "inpost_courier"
	PostKeyInPostTerminal = "inpost_terminal"
	PostKeyDHLCourier     = "dhl_courier"
	PostKeyPickup         = "pickup"
)

// Logical order-total keys, mapped to a shop's oc_order_total.code values.
const (
	TotalKeySubTotal = "sub_total"
	TotalKeyShipping = "shipping"
	TotalKeyCoupon   = "coupon"
	TotalKeyTax      = "tax"
	TotalKeyTotal    = "total"
	TotalKeyDiscount = "discount"
)

// SiteSettings is the resolved, validated description of the one OpenCart shop and Zoho picklist
// vocabulary this process serves. It is built once at startup by Config.SiteSettings() and then
// treated as immutable — read concurrently by the order poller, the customer poller and the HTTP
// handlers without further synchronisation.
type SiteSettings struct {
	Name string

	// Location is TimeZone already parsed; used for OpenCart datetime columns and Zoho timestamps.
	Location             *time.Location
	LanguageID           int
	LookbackDays         int
	BatchLimit           int
	PollInterval         time.Duration
	CustomerPollInterval time.Duration

	NipCustomFieldID  string
	PostTerminalField string
	ShippingItemUID   string
	Currencies        []string

	PollStatuses    []int
	StatusNew       int
	StatusCanceled  int
	b2bGroupIDs     map[int64]bool
	customerCats    map[int64]string
	totalCodes      map[string]string
	shippingCodeMap map[string]string

	Payments     bool
	CustomerSync bool
	B2B          bool

	ZohoLocation    string
	ZohoOrderSource string
	ZohoTerms       string
	ChunkSize       int
	B2BPipeline     string

	orderStatus    map[int]string
	orderStatusB2B map[int]string
	postTypes      map[string]string
	paymentStatus  map[string]string
}

// DefaultSiteSettings returns the settings the service behaved by before any of this was
// configurable — the first shop's values. Config.SiteSettings() starts from these and overlays
// whatever the YAML file supplies, so an existing config file keeps its exact behaviour.
func DefaultSiteSettings() *SiteSettings {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		// Only reachable on a system with no tzdata at all; UTC keeps the service usable.
		loc = time.UTC
	}
	return &SiteSettings{
		Name:                 "default",
		Location:             loc,
		LanguageID:           2,
		LookbackDays:         30,
		BatchLimit:           10,
		PollInterval:         2 * time.Minute,
		CustomerPollInterval: 5 * time.Minute,

		NipCustomFieldID:  "2",
		PostTerminalField: "field29",
		ShippingItemUID:   "cd3cc23c-6dfb-11ec-b75f-00155d018000",
		Currencies:        []string{"PLN", "EUR"},

		// Statuses 1, 2, 5, 17, 22, 23 — see the payment-link lifecycle note in entity/oc-order.go.
		PollStatuses:   []int{1, 2, 5, 17, 22, 23},
		StatusNew:      1,
		StatusCanceled: 7,
		b2bGroupIDs:    map[int64]bool{6: true, 16: true, 18: true, 19: true},
		customerCats: map[int64]string{
			20: "Інструктори",
			14: "Салони Європа",
			5:  "Салони Польща",
		},
		totalCodes: map[string]string{
			TotalKeySubTotal: "sub_total",
			TotalKeyShipping: "shipping",
			TotalKeyCoupon:   "coupon",
			TotalKeyTax:      "tax",
			TotalKeyTotal:    "total",
			TotalKeyDiscount: "discount",
		},
		shippingCodeMap: map[string]string{
			"filterit1.filterit0":     PostKeyInPostCourier,
			"filterit1.filterit1":     PostKeyInPostTerminal,
			"filterit0.filterit2":     PostKeyInPost,
			"filterit0.filterit0":     PostKeyDHLCourier,
			"filterit2.filterit0":     PostKeyDHLCourier,
			"filterit2.filterit1":     PostKeyDHLCourier,
			"dhl_country.dhl_country": PostKeyDHLCourier,
			"pickup.pickup":           PostKeyPickup,
		},

		Payments:     true,
		CustomerSync: true,
		B2B:          true,

		ZohoLocation:    "Польша",
		ZohoOrderSource: "OpenCart",
		ZohoTerms:       "Standard terms apply.",
		ChunkSize:       200,
		B2BPipeline:     "B2B",

		orderStatus: map[int]string{
			1:  "Нове",
			17: "Оплачено, формування ТТН",
			5:  "Перевірка та збір",
		},
		orderStatusB2B: map[int]string{
			1:  "Нове замовлення",
			17: "Оплачено формування ТТН",
			5:  "Передано на збір",
		},
		postTypes: map[string]string{
			PostKeyInPost:         "InPost",
			PostKeyInPostCourier:  "InPost (кур'єр)",
			PostKeyInPostTerminal: "InPost (поштомат)",
			PostKeyDHLCourier:     "DHL (кур'єр)",
			PostKeyPickup:         "Самовивіз",
		},
		paymentStatus: map[string]string{
			entity.PaymentKeyCreated:    "Створено",
			entity.PaymentKeyInProgress: "В процесі",
			entity.PaymentKeyHeld:       "Кошти зарезервовано",
			entity.PaymentKeyPaid:       "Оплачено",
			entity.PaymentKeyCanceled:   "Скасовано",
			entity.PaymentKeyRefunded:   "Відшкодовано",
			entity.PaymentKeyError:      "Помилка операції",
		},
	}
}

// SiteSettings resolves the site: and zoho: sections into validated settings, falling back to
// DefaultSiteSettings() for every key the file omits. Call once, at startup, and fail the process
// on error: a mistyped picklist key must stop the service, not surface as a silently empty field
// on the first order that reaches Zoho.
func (c *Config) SiteSettings() (*SiteSettings, error) {
	s := DefaultSiteSettings()
	site := c.Site

	if site.Name != "" {
		s.Name = site.Name
	}
	if site.TimeZone != "" {
		loc, err := time.LoadLocation(site.TimeZone)
		if err != nil {
			return nil, fmt.Errorf("site.timezone %q: %w", site.TimeZone, err)
		}
		s.Location = loc
	}
	if site.LanguageID != 0 {
		s.LanguageID = site.LanguageID
	}
	if site.LookbackDays != 0 {
		s.LookbackDays = site.LookbackDays
	}
	if site.BatchLimit != 0 {
		s.BatchLimit = site.BatchLimit
	}
	if site.PollInterval != 0 {
		s.PollInterval = time.Duration(site.PollInterval) * time.Second
	}
	if site.CustomerPollInterval != 0 {
		s.CustomerPollInterval = time.Duration(site.CustomerPollInterval) * time.Second
	}
	if site.NipCustomFieldID != "" {
		s.NipCustomFieldID = site.NipCustomFieldID
	}
	if site.PostTerminalField != "" {
		s.PostTerminalField = site.PostTerminalField
	}
	if site.ShippingItemUID != "" {
		s.ShippingItemUID = site.ShippingItemUID
	}
	if len(site.Currencies) > 0 {
		s.Currencies = site.Currencies
	}
	if len(site.OrderStatuses.Poll) > 0 {
		s.PollStatuses = site.OrderStatuses.Poll
	}
	if site.OrderStatuses.New != 0 {
		s.StatusNew = site.OrderStatuses.New
	}
	if site.OrderStatuses.Canceled != 0 {
		s.StatusCanceled = site.OrderStatuses.Canceled
	}
	// An explicitly empty b2b_group_ids list is meaningful (no group is B2B), so the presence of
	// the key is what matters, not its length. cleanenv gives a nil slice for an absent key.
	if site.B2BGroupIDs != nil {
		s.b2bGroupIDs = make(map[int64]bool, len(site.B2BGroupIDs))
		for _, id := range site.B2BGroupIDs {
			s.b2bGroupIDs[id] = true
		}
	}
	if site.CustomerCategories != nil {
		s.customerCats = site.CustomerCategories
	}
	// total_codes is merged, not replaced: a shop that renames one code should not have to
	// restate the other five.
	for k, v := range site.TotalCodes {
		if _, ok := s.totalCodes[k]; !ok {
			return nil, fmt.Errorf("site.total_codes: unknown key %q", k)
		}
		s.totalCodes[k] = v
	}
	if site.ShippingCodeMap != nil {
		s.shippingCodeMap = site.ShippingCodeMap
	}
	if site.Features.Payments != nil {
		s.Payments = *site.Features.Payments
	}
	if site.Features.CustomerSync != nil {
		s.CustomerSync = *site.Features.CustomerSync
	}
	if site.Features.B2B != nil {
		s.B2B = *site.Features.B2B
	}

	z := c.Zoho
	if z.Location != "" {
		s.ZohoLocation = z.Location
	}
	if z.OrderSource != "" {
		s.ZohoOrderSource = z.OrderSource
	}
	if z.Terms != "" {
		s.ZohoTerms = z.Terms
	}
	if z.ChunkSize != 0 {
		s.ChunkSize = z.ChunkSize
	}
	if z.B2BPipeline != "" {
		s.B2BPipeline = z.B2BPipeline
	}
	if z.OrderStatusMap != nil {
		s.orderStatus = z.OrderStatusMap
	}
	if z.OrderStatusB2BMap != nil {
		s.orderStatusB2B = z.OrderStatusB2BMap
	}
	if z.PostTypes != nil {
		s.postTypes = z.PostTypes
	}
	if z.PaymentStatuses != nil {
		s.paymentStatus = z.PaymentStatuses
	}

	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// validate rejects a configuration that would fail later, at the first order, in a place where the
// cause is far from the symptom.
func (s *SiteSettings) validate() error {
	if s.LanguageID <= 0 {
		return fmt.Errorf("site.language_id must be positive, got %d", s.LanguageID)
	}
	if s.LookbackDays <= 0 {
		return fmt.Errorf("site.lookback_days must be positive, got %d", s.LookbackDays)
	}
	if s.BatchLimit <= 0 {
		return fmt.Errorf("site.batch_limit must be positive, got %d", s.BatchLimit)
	}
	if s.PollInterval <= 0 {
		return fmt.Errorf("site.poll_interval must be positive")
	}
	if s.CustomerPollInterval <= 0 {
		return fmt.Errorf("site.customer_poll_interval must be positive")
	}
	if s.ChunkSize <= 0 {
		return fmt.Errorf("zoho.chunk_size must be positive, got %d", s.ChunkSize)
	}
	if s.ShippingItemUID == "" {
		return fmt.Errorf("site.shipping_item_uid must not be empty")
	}
	if len(s.Currencies) == 0 {
		return fmt.Errorf("site.currencies must not be empty")
	}
	if len(s.PollStatuses) == 0 {
		return fmt.Errorf("site.order_statuses.poll must not be empty")
	}
	if _, ok := s.orderStatus[s.StatusNew]; !ok {
		return fmt.Errorf("zoho.order_status_map has no entry for site.order_statuses.new (%d)", s.StatusNew)
	}
	// Every code a shipping module can produce must land on a post type this Zoho org knows,
	// otherwise the Post_type field silently stays empty for those orders.
	for code, key := range s.shippingCodeMap {
		if _, ok := s.postTypes[key]; !ok {
			return fmt.Errorf("site.shipping_code_map[%q] = %q, which is not a key of zoho.post_types", code, key)
		}
	}
	// The keyword rules can produce any of the five keys regardless of the code map, so the
	// picklist must cover all of them.
	for _, key := range []string{PostKeyInPost, PostKeyInPostCourier, PostKeyInPostTerminal, PostKeyDHLCourier, PostKeyPickup} {
		if _, ok := s.postTypes[key]; !ok {
			return fmt.Errorf("zoho.post_types is missing key %q", key)
		}
	}
	if s.Payments {
		for _, key := range entity.PaymentStatusKeys() {
			if _, ok := s.paymentStatus[key]; !ok {
				return fmt.Errorf("zoho.payment_statuses is missing key %q", key)
			}
		}
	}
	return nil
}

// IsB2B reports whether an OpenCart customer group is a B2B group on this shop. Always false when
// the B2B feature is off, so no order is diverted from the Sales_Orders sync.
func (s *SiteSettings) IsB2B(groupId int64) bool {
	if !s.B2B {
		return false
	}
	return s.b2bGroupIDs[groupId]
}

// CustomerCategory maps an OpenCart customer_group_id to the Zoho customer_category value.
// Returns an empty string for an unmapped group, so omitempty drops the field.
func (s *SiteSettings) CustomerCategory(groupId int64) string {
	return s.customerCats[groupId]
}

// TotalCode returns this shop's oc_order_total.code for a logical total (TotalKey* constants).
func (s *SiteSettings) TotalCode(key string) string {
	if code, ok := s.totalCodes[key]; ok {
		return code
	}
	return key
}

// AllowedCurrency reports whether an order's currency is one this shop sells in. Replaces the
// compile-time `oneof=PLN EUR` validation tag, which could not vary per site.
func (s *SiteSettings) AllowedCurrency(code string) bool {
	for _, c := range s.Currencies {
		if c == code {
			return true
		}
	}
	return false
}

// OrderStatusName maps an OpenCart order_status_id to the Zoho Sales Order Status picklist value.
//
// Not every polled status needs a mapping — the payment-link statuses, for instance, describe
// where an order sits in the wfsync flow, not a state Zoho has a name for. Those fall back to the
// new-order status, which is what the Sales Order carried before any of this was configurable.
// validate() guarantees that fallback exists, so this never returns "".
func (s *SiteSettings) OrderStatusName(statusId int) string {
	if name, ok := s.orderStatus[statusId]; ok {
		return name
	}
	return s.orderStatus[s.StatusNew]
}

// OrderStatusB2BName is OrderStatusName for the B2B Deals pipeline. Unmapped statuses fall back
// the same way; an empty result means the site defines no B2B status map at all.
func (s *SiteSettings) OrderStatusB2BName(statusId int) string {
	if name, ok := s.orderStatusB2B[statusId]; ok {
		return name
	}
	return s.orderStatusB2B[s.StatusNew]
}

// OrderStatusIdByName is the reverse of OrderStatusName, used to translate a status arriving on a
// Zoho webhook back into an OpenCart order_status_id. Returns -1 when the name is unknown.
// Iteration order over the map is random, so ties are broken by taking the lowest id — a
// deterministic answer beats an arbitrary one when a shop maps two ids to one picklist value.
func (s *SiteSettings) OrderStatusIdByName(statusName string) int {
	found := -1
	for id, name := range s.orderStatus {
		if name == statusName && (found == -1 || id < found) {
			found = id
		}
	}
	return found
}

// PaymentStatus maps a Stripe/wfsync payment status string (as wfsync wrote it into
// oc_order.wf_payment_status) to this Zoho org's Payments status picklist value.
func (s *SiteSettings) PaymentStatus(stripeStatus string) string {
	return s.paymentStatus[entity.PaymentStatusKey(stripeStatus)]
}

// PostType maps an OpenCart shipping method to this Zoho org's Post_type picklist value.
//
// The method name is matched first: shipping_method is stored in the customer's language
// (Ukrainian, Polish or English), so matching is done on lowercased keywords across all three.
// shipping_code is only a fallback, because production data holds rows where the code and the
// name disagree (e.g. a "DHL Kurier" order carrying the InPost courier code).
//
// Returns an empty string when nothing matches, so omitempty drops the field and the record stays
// at -None- for a manager to fill in.
func (s *SiteSettings) PostType(shippingCode, shippingMethod string) string {
	if key := postKeyFromMethod(shippingMethod); key != "" {
		return s.postTypes[key]
	}
	if key, ok := s.shippingCodeMap[shippingCode]; ok {
		return s.postTypes[key]
	}
	return ""
}

// postKeyFromMethod derives a logical post-type key from a shipping method name in any of the
// three languages OpenCart stores it in. Returns "" when the name says nothing recognisable.
func postKeyFromMethod(shippingMethod string) string {
	m := strings.ToLower(shippingMethod)
	switch {
	case strings.Contains(m, "inpost"):
		// Paczkomat (pl) / поштомат (ua) / parcel machine (en)
		if strings.Contains(m, "paczkomat") || strings.Contains(m, "поштомат") || strings.Contains(m, "parcel machine") {
			return PostKeyInPostTerminal
		}
		if isCourier(m) {
			return PostKeyInPostCourier
		}
		return PostKeyInPost
	case strings.Contains(m, "dhl"):
		return PostKeyDHLCourier
	case strings.Contains(m, "pickup") || strings.Contains(m, "odbiór") || strings.Contains(m, "самовивіз"):
		return PostKeyPickup
	}
	return ""
}

// isCourier reports whether a lowercased shipping method name says "courier" in any of the three
// languages OpenCart stores it in.
func isCourier(m string) bool {
	return strings.Contains(m, "kurier") || strings.Contains(m, "кур'єр") || strings.Contains(m, "courier")
}

// LogValue renders the resolved settings for the startup log, so a deployment can be checked
// against what the process actually decided rather than against what the file appears to say.
func (s *SiteSettings) LogValue() string {
	sorted := append([]int(nil), s.PollStatuses...)
	sort.Ints(sorted)
	statuses := make([]string, 0, len(sorted))
	for _, st := range sorted {
		statuses = append(statuses, fmt.Sprintf("%d", st))
	}
	return fmt.Sprintf(
		"name=%s tz=%s lang=%d lookback=%dd limit=%d poll=%s statuses=[%s] currencies=[%s] "+
			"payments=%t customers=%t b2b=%t location=%s source=%s",
		s.Name, s.Location, s.LanguageID, s.LookbackDays, s.BatchLimit, s.PollInterval,
		strings.Join(statuses, ","), strings.Join(s.Currencies, ","),
		s.Payments, s.CustomerSync, s.B2B, s.ZohoLocation, s.ZohoOrderSource,
	)
}
