// Package address contains the postal address value object used by assets.
package address

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var digits = regexp.MustCompile(`\D`)
var postalCodePattern = regexp.MustCompile(`^(\d{8}|\d{5}-\d{3})$`)
var municipalityCodePattern = regexp.MustCompile(`^\d{7}$`)

// Details is a Brazilian postal address. Country is deliberately fixed by the
// application for this version and is never collected from a browser.
type Details struct {
	CountryCode      string `json:"countryCode"`
	PostalCode       string `json:"postalCode"`
	Street           string `json:"street"`
	Number           string `json:"number"`
	WithoutNumber    bool   `json:"withoutNumber"`
	Complement       string `json:"complement"`
	District         string `json:"district"`
	City             string `json:"city"`
	State            string `json:"state"`
	MunicipalityCode string `json:"municipalityCode"`
	Reference        string `json:"reference"`
}

var states = map[string]struct{}{
	"AC": {}, "AL": {}, "AP": {}, "AM": {}, "BA": {}, "CE": {}, "DF": {}, "ES": {}, "GO": {}, "MA": {}, "MT": {}, "MS": {}, "MG": {}, "PA": {}, "PB": {}, "PR": {}, "PE": {}, "PI": {}, "RJ": {}, "RN": {}, "RS": {}, "RO": {}, "RR": {}, "SC": {}, "SP": {}, "SE": {}, "TO": {},
}

// Normalize trims fields, fixes the Brazilian country code and normalizes CEP
// and UF without changing accents or the user's letter case in place names.
func Normalize(value Details) Details {
	value.CountryCode = "BR"
	value.PostalCode = digits.ReplaceAllString(value.PostalCode, "")
	value.Street = clean(value.Street)
	value.Number = clean(value.Number)
	value.Complement = clean(value.Complement)
	value.District = clean(value.District)
	value.City = clean(value.City)
	value.State = strings.ToUpper(clean(value.State))
	value.MunicipalityCode = digits.ReplaceAllString(value.MunicipalityCode, "")
	value.Reference = clean(value.Reference)
	return value
}

// Validate checks all required fields and lengths for a complete address.
func Validate(input Details) (Details, error) {
	value := Normalize(input)
	if !postalCodePattern.MatchString(strings.TrimSpace(input.PostalCode)) || len(value.PostalCode) != 8 {
		return Details{}, fmt.Errorf("CEP deve conter oito dígitos")
	}
	if value.Street == "" || size(value.Street) > 200 {
		return Details{}, fmt.Errorf("logradouro é obrigatório e deve ter até 200 caracteres")
	}
	if value.WithoutNumber && value.Number != "" {
		return Details{}, fmt.Errorf("informe o número ou marque sem número")
	}
	if !value.WithoutNumber && value.Number == "" {
		return Details{}, fmt.Errorf("informe o número ou marque sem número")
	}
	if size(value.Number) > 30 || size(value.Complement) > 200 || size(value.District) > 120 || size(value.City) == 0 || size(value.City) > 120 || size(value.Reference) > 300 {
		return Details{}, fmt.Errorf("um ou mais campos do endereço excedem o limite permitido")
	}
	if _, ok := states[value.State]; !ok {
		return Details{}, fmt.Errorf("informe uma UF brasileira válida")
	}
	if value.MunicipalityCode != "" && !municipalityCodePattern.MatchString(strings.TrimSpace(input.MunicipalityCode)) {
		return Details{}, fmt.Errorf("código do município inválido")
	}
	return value, nil
}

// Format returns the canonical human-readable projection for this address.
func Format(value Details) string {
	value = Normalize(value)
	street := value.Street
	if value.WithoutNumber {
		street += ", s/n"
	} else if value.Number != "" {
		street += ", " + value.Number
	}
	if value.Complement != "" {
		street += ", " + value.Complement
	}
	locality := strings.Join(nonEmpty(value.District, value.City), " — ")
	if value.State != "" {
		locality += "/" + value.State
	}
	parts := nonEmpty(street, locality)
	if value.PostalCode != "" {
		postal := value.PostalCode
		if len(postal) == 8 {
			postal = postal[:5] + "-" + postal[5:]
		}
		parts = append(parts, "CEP "+postal)
	}
	return strings.Join(parts, " — ")
}

func clean(value string) string { return strings.Join(strings.Fields(strings.TrimSpace(value)), " ") }
func size(value string) int     { return utf8.RuneCountInString(value) }
func nonEmpty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
