package config

import (
	"fmt"
	"log"
	"sync"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	Env string `yaml:"env" env-default:"local" env-required:"true"`
	SQL struct {
		Enabled  bool   `yaml:"enabled" env-default:"false"`
		Driver   string `yaml:"driver" env-default:"mysql"`
		HostName string `yaml:"hostname" env-default:"localhost"`
		UserName string `yaml:"username" env-default:"root"`
		Password string `yaml:"password" env-default:""`
		Database string `yaml:"database" env-default:""`
		Port     string `yaml:"port" env-default:"8080"`
		Prefix   string `yaml:"prefix" env-default:""`
	} `yaml:"sql"`
	Mongo struct {
		Enabled     bool   `yaml:"enabled" env-default:"false"`
		Host        string `yaml:"host" env-default:"127.0.0.1"`
		Port        string `yaml:"port" env-default:"27017"`
		User        string `yaml:"user" env-default:"admin"`
		Password    string `yaml:"password" env-default:"pass"`
		Database    string `yaml:"database" env-default:""`
		ExpiredDays int    `yaml:"expired_days" env-default:"7"`
	} `yaml:"mongo"`
	Telegram struct {
		Enabled     bool   `yaml:"enabled" env-default:"false"`
		ApiKey      string `yaml:"api_key" env-default:""`
		AdminId     string `yaml:"admin_id" env-default:""`
		BotName     string `yaml:"bot_name" env-default:"ZohoBot"`
		MinLogLevel string `yaml:"min_log_level" env-default:"debug"`
	} `yaml:"telegram"`
	// Site describes everything that differs between the OpenCart shops this binary can serve.
	// One process serves one site; a second shop runs a second process with its own config file.
	// Every field is optional — an omitted key falls back to the value the first shop used when
	// these were Go constants, so an existing config file keeps working unchanged.
	// Resolved and validated by SiteSettings().
	Site struct {
		Name string `yaml:"name" env-default:""`
		// LogFile names this instance's log file inside the -log directory. Read directly rather
		// than through SiteSettings, because the logger is built before the settings are resolved.
		LogFile  string `yaml:"log_file" env-default:""`
		TimeZone string `yaml:"timezone" env-default:""`
		// LanguageID selects the oc_product_description / oc_order_product language rows.
		LanguageID int `yaml:"language_id" env-default:"0"`
		// LookbackDays bounds how far back the order poller looks at date_modified.
		LookbackDays int `yaml:"lookback_days" env-default:"0"`
		// BatchLimit caps how many orders one poll of a single status returns.
		BatchLimit int `yaml:"batch_limit" env-default:"0"`
		// PollInterval / CustomerPollInterval are in seconds.
		PollInterval         int `yaml:"poll_interval" env-default:"0"`
		CustomerPollInterval int `yaml:"customer_poll_interval" env-default:"0"`
		// NipCustomFieldID is the oc_custom_field id holding the buyer's tax number.
		NipCustomFieldID string `yaml:"nip_custom_field_id" env-default:""`
		// PostTerminalField is the oc_order_simple_fields column holding the parcel-locker code.
		PostTerminalField string `yaml:"post_terminal_field" env-default:""`
		// ShippingItemUID is the oc_product.product_uid of the pseudo-product carriage is billed as.
		ShippingItemUID string   `yaml:"shipping_item_uid" env-default:""`
		Currencies      []string `yaml:"currencies"`
		OrderStatuses   struct {
			// Poll lists the oc_order.order_status_id values that trigger a Zoho sync.
			Poll     []int `yaml:"poll"`
			New      int   `yaml:"new" env-default:"0"`
			Canceled int   `yaml:"canceled" env-default:"0"`
		} `yaml:"order_statuses"`
		B2BGroupIDs []int64 `yaml:"b2b_group_ids"`
		// CustomerCategories maps oc_customer.customer_group_id to the Zoho customer_category value.
		CustomerCategories map[int64]string `yaml:"customer_categories"`
		// TotalCodes maps a logical total to its oc_order_total.code on this shop.
		TotalCodes map[string]string `yaml:"total_codes"`
		Features   struct {
			// Payments covers the wfsync wf_payment_* columns and the Zoho Payments module.
			Payments *bool `yaml:"payments"`
			// CustomerSync covers the oc_customer -> Zoho Contacts upsert loop.
			CustomerSync *bool `yaml:"customer_sync"`
			// B2B covers customer_group_id routing and the Zoho Deals/Goods modules.
			B2B *bool `yaml:"b2b"`
		} `yaml:"features"`
		// ShippingCodeMap maps an OpenCart shipping module code to a logical post-type key,
		// which Zoho.PostTypes then resolves to a picklist value.
		ShippingCodeMap map[string]string `yaml:"shipping_code_map"`
	} `yaml:"site"`
	Zoho struct {
		ClientId     string `yaml:"client_id" env-default:""`
		ClientSecret string `yaml:"client_secret" env-default:""`
		RefreshToken string `yaml:"refresh_token" env-default:""`
		RefreshUrl   string `yaml:"refresh_url" env-default:""`
		CrmUrl       string `yaml:"crm_url" env-default:""`
		Scope        string `yaml:"scope" env-default:""`
		ApiVersion   string `yaml:"api_version" env-default:""`
		// Picklist values and literals written onto Zoho records. Same Zoho org for every site,
		// so field API names are fixed in entity/ — only these values vary.
		Location    string `yaml:"location" env-default:""`
		OrderSource string `yaml:"order_source" env-default:""`
		Terms       string `yaml:"terms" env-default:""`
		// ChunkSize caps how many subform rows go into one Sales Order API call.
		ChunkSize   int    `yaml:"chunk_size" env-default:"0"`
		B2BPipeline string `yaml:"b2b_pipeline" env-default:""`
		// OrderStatusMap maps an OpenCart order_status_id to the Zoho Sales Order Status picklist.
		// Also used in reverse to translate an inbound webhook status back to an OpenCart id.
		OrderStatusMap    map[int]string `yaml:"order_status_map"`
		OrderStatusB2BMap map[int]string `yaml:"order_status_b2b_map"`
		// PostTypes maps a logical post-type key to the Zoho Post_type picklist value.
		PostTypes map[string]string `yaml:"post_types"`
		// PaymentStatuses maps a logical payment state to the Zoho Payments status picklist.
		PaymentStatuses map[string]string `yaml:"payment_statuses"`
	} `yaml:"zoho"`
	ProdRepo struct {
		Login    string `yaml:"login" env-default:""`
		Password string `yaml:"password" env-default:""`
		ProdUrl  string `yaml:"prod_url" env-default:""`
	} `yaml:"prod_repo"`
	Listen struct {
		BindIP string `yaml:"bind_ip" env-default:"127.0.0.1"`
		Port   string `yaml:"port" env:"PORT" env-default:"8080"`
		ApiKey string `yaml:"key" env-default:""`
	} `yaml:"listen"`
	SmartSender struct {
		Enabled      bool   `yaml:"enabled" env-default:"false"`
		ApiKey       string `yaml:"api_key" env-default:""`
		BaseURL      string `yaml:"base_url" env-default:"https://api.smartsender.com/v1"`
		ZohoApiKey   string `yaml:"zoho_api_key" env-default:""`
		ZohoMsgURL   string `yaml:"zoho_msg_url" env-default:""`
		PollInterval int    `yaml:"poll_interval" env-default:"60"`
	} `yaml:"smartsender"`
}

var instance *Config
var once sync.Once

func MustLoad(path string) *Config {
	var err error
	once.Do(func() {
		instance = &Config{}
		if err = cleanenv.ReadConfig(path, instance); err != nil {
			desc, _ := cleanenv.GetDescription(instance, nil)
			err = fmt.Errorf("%s; %s", err, desc)
			instance = nil
			log.Fatal(err)
		}
	})
	return instance
}
