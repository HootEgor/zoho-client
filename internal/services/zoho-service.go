package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"zohoapi/entity"
	"zohoapi/internal/config"
)

type ZohoAPIResponse struct {
	Data []ZohoResponseItem `json:"data"`
}

type ZohoResponseItem struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Code    string         `json:"code"`
	Details ZohoRecordInfo `json:"details"`
}

type ZohoRecordInfo struct {
	ID           string       `json:"id"`
	CreatedBy    ZohoUserInfo `json:"Created_By"`
	CreatedTime  string       `json:"Created_Time"`
	ModifiedBy   ZohoUserInfo `json:"Modified_By"`
	ModifiedTime string       `json:"Modified_Time"`
}

type ZohoUserInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ZohoService struct {
	clientID     string
	clientSecret string
	refreshToken string
	refreshUrl   string
	crmUrl       string
}

func NewZohoService(conf *config.Config) (*ZohoService, error) {

	service := &ZohoService{
		clientID:     conf.Zoho.ClientId,
		clientSecret: conf.Zoho.ClientSecret,
		refreshToken: conf.Zoho.RefreshToken,
		refreshUrl:   conf.Zoho.RefreshUrl,
		crmUrl:       conf.Zoho.CrmUrl,
	}

	return service, nil
}

func (s *ZohoService) RefreshToken() error {
	form := url.Values{}
	form.Add("client_id", s.clientID)
	form.Add("client_secret", s.clientSecret)
	form.Add("refresh_token", s.refreshToken)
	form.Add("grant_type", "refresh_token")

	resp, err := http.PostForm(s.refreshUrl, form)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("refresh token failed: %s", string(bodyBytes))
	}

	var response ZohoAPIResponse
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	for _, item := range response.Data {
		fmt.Printf("Status: %s, Record ID: %s, Created By: %s\n",
			item.Status,
			item.Details.ID,
			item.Details.CreatedBy.Name,
		)
	}

	if len(response.Data) == 0 && response.Data[0].Status != "success" {
		return fmt.Errorf("failed to refresh token: %s", response.Data[0].Message)
	}

	s.refreshToken = response.Data[0].Details.CreatedBy.ID

	return nil
}

func (s *ZohoService) CreateContact(contactData entity.Contact) (string, error) {
	// Ensure phone is numeric only
	re := regexp.MustCompile(`\D`)
	contactData.Phone = re.ReplaceAllString(contactData.Phone, "")

	// Prepare request body
	payload := map[string]interface{}{
		"data": []entity.Contact{contactData},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(
		http.MethodPost,
		s.crmUrl+"/Contacts",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.refreshToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var apiResp ZohoAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(apiResp.Data) == 0 || apiResp.Data[0].Status != "success" {
		return "", fmt.Errorf("contact not created or failed: %+v", apiResp.Data)
	}

	return apiResp.Data[0].Details.CreatedBy.ID, nil
}

func (s *ZohoService) CreateOrder(orderData entity.ZohoOrder) (string, error) {
	// Prepare payload
	payload := map[string]interface{}{
		"data": []entity.ZohoOrder{orderData},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	// Build HTTP request
	req, err := http.NewRequest(
		http.MethodPost,
		s.crmUrl+"/Sales_Orders",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+s.refreshToken)
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status code: %d - %s", resp.StatusCode, respBody)
	}

	// Parse response
	var apiResp ZohoAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(apiResp.Data) == 0 || apiResp.Data[0].Status != "success" {
		return "", fmt.Errorf("order not created or failed: %+v", apiResp.Data)
	}

	return apiResp.Data[0].Details.CreatedBy.ID, nil
}
