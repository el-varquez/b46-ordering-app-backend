package inventoryhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
)

type doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Source struct {
	client           doer
	endpoint         string
	token            string
	maxResponseBytes int64
}

func New(client doer, baseURL, token string, maxResponseBytes int64) (*Source, error) {
	if client == nil || token == "" || maxResponseBytes <= 0 {
		return nil, errors.New("catalog HTTP source requires client, token, and response limit")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("inventory adapter URL must be absolute HTTP(S)")
	}
	endpoint, err := url.JoinPath(parsed.String(), "catalog", "products")
	if err != nil {
		return nil, fmt.Errorf("build catalog endpoint: %w", err)
	}
	return &Source{client: client, endpoint: endpoint, token: token, maxResponseBytes: maxResponseBytes}, nil
}

func (source *Source) Products(ctx context.Context, limit int, afterID string) (domain.Page, error) {
	endpoint, err := url.Parse(source.endpoint)
	if err != nil {
		return domain.Page{}, fmt.Errorf("%w: parse catalog endpoint", domain.ErrUnavailable)
	}
	query := endpoint.Query()
	query.Set("limit", strconv.Itoa(limit))
	if afterID != "" {
		query.Set("after_id", afterID)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return domain.Page{}, fmt.Errorf("%w: build request", domain.ErrUnavailable)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+source.token)
	response, err := source.client.Do(request)
	if err != nil {
		return domain.Page{}, fmt.Errorf("%w: request catalog: %v", domain.ErrUnavailable, err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, source.maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || int64(len(body)) > source.maxResponseBytes {
		return domain.Page{}, fmt.Errorf("%w: read catalog response", domain.ErrUnavailable)
	}
	if response.StatusCode != http.StatusOK {
		return domain.Page{}, fmt.Errorf("%w: catalog HTTP status %d", domain.ErrUnavailable, response.StatusCode)
	}
	var wire struct {
		Products []struct {
			ProductID       string `json:"product_id"`
			Name            string `json:"name"`
			Description     string `json:"description"`
			PriceCentavos   int64  `json:"price_centavos"`
			CategoryID      string `json:"category_id"`
			CategoryName    string `json:"category_name"`
			Available       bool   `json:"available"`
			SourceUpdatedAt string `json:"source_updated_at"`
		} `json:"products"`
		NextAfterID string `json:"next_after_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return domain.Page{}, fmt.Errorf("%w: decode catalog response", domain.ErrUnavailable)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return domain.Page{}, fmt.Errorf("%w: trailing catalog response", domain.ErrUnavailable)
	}
	page := domain.Page{NextAfterID: wire.NextAfterID, Products: make([]domain.Product, 0, len(wire.Products))}
	for _, value := range wire.Products {
		updatedAt, parseErr := time.Parse(time.RFC3339Nano, value.SourceUpdatedAt)
		if parseErr != nil {
			return domain.Page{}, fmt.Errorf("%w: invalid source timestamp", domain.ErrUnavailable)
		}
		product := domain.Product{ID: value.ProductID, Name: value.Name, Description: value.Description,
			PriceCentavos: value.PriceCentavos, CategoryID: value.CategoryID, CategoryName: value.CategoryName,
			Available: value.Available, SourceUpdatedAt: updatedAt.UTC()}
		if err := product.Validate(); err != nil {
			return domain.Page{}, fmt.Errorf("%w: invalid source product", domain.ErrUnavailable)
		}
		page.Products = append(page.Products, product)
	}
	return page, nil
}
