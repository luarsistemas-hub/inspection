package address

import "testing"

func TestValidateAndFormatBrazilianAddress(t *testing.T) {
	value, err := Validate(Details{PostalCode: "88000-000", Street: " Rua   das Flores ", Number: "12A", Complement: "Apto 2", District: "Centro", City: "Florianópolis", State: "sc"})
	if err != nil {
		t.Fatal(err)
	}
	if value.CountryCode != "BR" || value.PostalCode != "88000000" {
		t.Fatalf("normalized address: %#v", value)
	}
	if got, want := Format(value), "Rua das Flores, 12A, Apto 2 — Centro — Florianópolis/SC — CEP 88000-000"; got != want {
		t.Fatalf("Format() = %q, want %q", got, want)
	}
}

func TestValidateWithoutNumberAndRejectInvalidAddress(t *testing.T) {
	base := Details{PostalCode: "01001000", Street: "Praça da Sé", WithoutNumber: true, City: "São Paulo", State: "SP"}
	if _, err := Validate(base); err != nil {
		t.Fatalf("rural or unnumbered address rejected: %v", err)
	}
	base.Number = "0"
	if _, err := Validate(base); err == nil {
		t.Fatal("number combined with withoutNumber was accepted")
	}
	base.Number, base.WithoutNumber, base.PostalCode = "", false, "123"
	if _, err := Validate(base); err == nil {
		t.Fatal("invalid CEP and missing number were accepted")
	}
}
