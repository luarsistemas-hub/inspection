package lookup_postal_code

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var cepPattern = regexp.MustCompile(`^\d{8}$`)

const maxLookupEntries = 4096

type Result struct {
	Found            bool
	PostalCode       string
	Street           *string
	District         *string
	City             *string
	State            *string
	MunicipalityCode *string
}

type Service struct {
	Client *http.Client
	Now    func() time.Time
	mu     sync.Mutex
	cache  map[string]cacheEntry
	limits map[string][]time.Time
}

type cacheEntry struct {
	result  Result
	expires time.Time
}

// Lookup returns Brazilian postal components, with an in-memory success and
// negative-result cache. Provider failure is returned so callers can continue
// with the manual form.
func (s *Service) Lookup(ctx context.Context, postalCode, clientKey string) (Result, error) {
	cep := strings.TrimSpace(postalCode)
	if !cepPattern.MatchString(cep) {
		return Result{}, fmt.Errorf("CEP deve conter oito dígitos")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	if clientKey != "" && !s.allow(clientKey, now) {
		return Result{}, fmt.Errorf("limite de consultas atingido; tente novamente em instantes")
	}
	s.mu.Lock()
	if entry, ok := s.cache[cep]; ok && now.Before(entry.expires) {
		s.mu.Unlock()
		return entry.result, nil
	}
	s.mu.Unlock()
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://viacep.com.br/ws/"+cep+"/json/", nil)
	if err != nil {
		return Result{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("consulta de CEP indisponível")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("consulta de CEP indisponível")
	}
	var payload struct {
		CEP              string `json:"cep"`
		Street           string `json:"logradouro"`
		District         string `json:"bairro"`
		City             string `json:"localidade"`
		State            string `json:"uf"`
		MunicipalityCode string `json:"ibge"`
		Error            any    `json:"erro"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("resposta de CEP inválida")
	}
	if payload.Error == true || payload.Error == "true" {
		result := Result{Found: false, PostalCode: cep}
		s.save(cep, result, now.Add(5*time.Minute), now)
		return result, nil
	}
	if payload.CEP == "" || payload.City == "" || payload.State == "" {
		return Result{}, fmt.Errorf("resposta de CEP incompleta")
	}
	result := Result{Found: true, PostalCode: cep, Street: nonEmptyPointer(payload.Street), District: nonEmptyPointer(payload.District), City: nonEmptyPointer(payload.City), State: nonEmptyPointer(payload.State), MunicipalityCode: nonEmptyPointer(payload.MunicipalityCode)}
	s.save(cep, result, now.Add(24*time.Hour), now)
	return result, nil
}

func (s *Service) allow(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limits == nil {
		s.limits = map[string][]time.Time{}
	}
	if _, known := s.limits[key]; !known && len(s.limits) >= maxLookupEntries {
		for client, attempts := range s.limits {
			if len(attempts) == 0 || now.Sub(attempts[len(attempts)-1]) >= time.Minute {
				delete(s.limits, client)
			}
		}
		if len(s.limits) >= maxLookupEntries {
			return false
		}
	}
	items := s.limits[key][:0]
	for _, item := range s.limits[key] {
		if now.Sub(item) < time.Minute {
			items = append(items, item)
		}
	}
	if len(items) >= 30 {
		s.limits[key] = items
		return false
	}
	s.limits[key] = append(items, now)
	return true
}
func (s *Service) save(key string, result Result, expires, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]cacheEntry{}
	}
	if _, known := s.cache[key]; !known && len(s.cache) >= maxLookupEntries {
		for postal, entry := range s.cache {
			if !entry.expires.After(now) {
				delete(s.cache, postal)
			}
		}
		if len(s.cache) >= maxLookupEntries {
			for postal := range s.cache {
				delete(s.cache, postal)
				break
			}
		}
	}
	s.cache[key] = cacheEntry{result: result, expires: expires}
}
func nonEmptyPointer(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
