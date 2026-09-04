package services

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/lib/sl"
)

type ProductRepo struct {
	login    string
	password string
	// productUrl is the repository's product endpoint. siteCode, when set, is inserted as a path
	// segment before the product UID: the repository holds one Zoho product id per site, and
	// without the code it answers with the default site's ids.
	productUrl string
	siteCode   string
	log        *slog.Logger
}

func NewProductRepo(conf *config.Config, log *slog.Logger) (*ProductRepo, error) {
	service := &ProductRepo{
		login:      conf.ProdRepo.Login,
		password:   conf.ProdRepo.Password,
		productUrl: conf.ProdRepo.ProdUrl,
		siteCode:   conf.ProdRepo.SiteCode,
		log:        log.With(sl.Module("product-repo")),
	}

	return service, nil
}

func (p *ProductRepo) getBase64Auth() string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", p.login, p.password)))
}

func (p *ProductRepo) GetProductZohoID(productUID string) (string, error) {
	if productUID == "" {
		return "", fmt.Errorf("product UID is empty")
	}

	// .../product/{site_code}/{uid} once a site code is configured, .../product/{uid} without one.
	// The unscoped form is kept so a deployment keeps working until the repository's per-site
	// endpoint is live and the code is set — it answers with the default site's ids.
	segments := []string{productUID}
	if p.siteCode != "" {
		segments = []string{p.siteCode, productUID}
	}

	fullURL, err := buildURL(p.productUrl, segments...)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Basic %s", p.getBase64Auth()))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var apiResp entity.ProductResponse
	if err = json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if !apiResp.Success {
		return "", fmt.Errorf("API returned error: %s", apiResp.Message)
	}

	return apiResp.Data.Id, nil
}
