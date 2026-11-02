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

func TestValidateRejectsControlAndFormatRunesAndComposesNFC(t *testing.T) {
	base := Details{PostalCode: "01001000", Street: "Praça da Sé", Number: "1", City: "São Paulo", State: "SP"}
	for name, mutate := range map[string]func(*Details){
		"NUL":  func(d *Details) { d.Street = "Rua\x00 A" },
		"bidi": func(d *Details) { d.City = "Cidade‮" },
		"zwsp": func(d *Details) { d.Reference = "perto​" },
		"utf8": func(d *Details) { d.Complement = "\xff" },
	} {
		value := base
		mutate(&value)
		if _, err := Validate(value); err == nil {
			t.Fatalf("%s address text was accepted", name)
		}
	}
	value := base
	value.Street = "Rua José\ttab"
	got, err := Validate(value)
	if err != nil {
		t.Fatal(err)
	}
	if got.Street != "Rua José tab" {
		t.Fatalf("street = %q, want NFC with collapsed whitespace", got.Street)
	}
}

func TestCleanTextCanonicalizesAndRejectsUnsafeRunes(t *testing.T) {
	if got, ok := CleanText("  Aná   Maria "); !ok || got != "Aná Maria" {
		t.Fatalf("CleanText() = %q, %v", got, ok)
	}
	for _, value := range []string{"a\x00b", "a‮b", "a​b", "\xffa"} {
		if _, ok := CleanText(value); ok {
			t.Fatalf("CleanText(%q) accepted unsafe text", value)
		}
	}
}
