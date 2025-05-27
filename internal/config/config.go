package config

import (
	"fmt"
	"github.com/ilyakaznacheev/cleanenv"
	"log"
	"sync"
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
	Telegram struct {
		Enabled bool   `yaml:"enabled" env-default:"false"`
		ApiKey  string `yaml:"api_key" env-default:""`
	}
	Zoho struct {
		ClientId     string `yaml:"client_id" env-default:""`
		ClientSecret string `yaml:"client_secret" env-default:""`
		RefreshToken string `yaml:"refresh_token" env-default:""`
		RefreshUrl   string `yaml:"refresh_url" env-default:""`
		CrmUrl       string `yaml:"crm_url" env-default:""`
		Scope        string `yaml:"scope" env-default:""`
		ApiVersion   string `yaml:"api_version" env-default:""`
	} `yaml:"zoho"`
	ProdRepo struct {
		Login    string `yaml:"login" env-default:""`
		Password string `yaml:"password" env-default:""`
		ProdUrl  string `yaml:"prod_url" env-default:""`
	} `yaml:"prod_repo"`
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
