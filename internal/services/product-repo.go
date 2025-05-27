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
	login      string
	password   string
	productUrl string
	log        *slog.Logger
}

func NewProductRepo(conf *config.Config, log *slog.Logger) (*ProductRepo, error) {
	service := &ProductRepo{
		login:      conf.ProdRepo.Login,
		password:   conf.ProdRepo.Password,
		productUrl: conf.ProdRepo.ProdUrl,
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

	fullURL, err := buildURL(p.productUrl, productUID)
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
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	p.log.With(
		slog.String("product_uid", productUID),
		slog.String("response", string(bodyBytes)),
	).Debug("get product zoho id response")

	var apiResp entity.ProductResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if !apiResp.Success {
		return "", fmt.Errorf("API returned error: %s", apiResp.Message)
	}

	return apiResp.Data.Id, nil
}
