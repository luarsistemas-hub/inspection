// Package core defines the controlled vocabulary and pure validation rules for inventories.
package vocabulary

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
)

// Field limits shared by validation and the inventory tables.
const (
	MaxEnvironments        = 60
	MaxItemsPerEnvironment = 80
	MaxItemsPerVersion     = 1000
	MaxNameRunes           = 80
	MaxMaterialRunes       = 200
	MaxColorRunes          = 100
	MaxLimitationRunes     = 200
	MinQuantity            = 1
	MaxQuantity            = 999
)

// EnvironmentType is a supported environment code.
type EnvironmentType string

// ItemType is a supported inventory item code.
type ItemType string

// Condition is a level of the fixed inventory condition scale.
type Condition string

// Observation states how much the evidence supports observing an item.
type Observation string

const (
	LivingRoom       EnvironmentType = "LIVING_ROOM"
	DiningRoom       EnvironmentType = "DINING_ROOM"
	Kitchen          EnvironmentType = "KITCHEN"
	Bedroom          EnvironmentType = "BEDROOM"
	Suite            EnvironmentType = "SUITE"
	Bathroom         EnvironmentType = "BATHROOM"
	Toilet           EnvironmentType = "TOILET"
	Laundry          EnvironmentType = "LAUNDRY"
	ServiceArea      EnvironmentType = "SERVICE_AREA"
	Balcony          EnvironmentType = "BALCONY"
	Garage           EnvironmentType = "GARAGE"
	Hallway          EnvironmentType = "HALLWAY"
	Entrance         EnvironmentType = "ENTRANCE"
	Office           EnvironmentType = "OFFICE"
	Storage          EnvironmentType = "STORAGE"
	ExternalArea     EnvironmentType = "EXTERNAL_AREA"
	PoolArea         EnvironmentType = "POOL_AREA"
	GourmetArea      EnvironmentType = "GOURMET_AREA"
	OtherEnvironment EnvironmentType = "OTHER"
)

const (
	Floor          ItemType = "FLOOR"
	Wall           ItemType = "WALL"
	Ceiling        ItemType = "CEILING"
	Door           ItemType = "DOOR"
	Window         ItemType = "WINDOW"
	Lighting       ItemType = "LIGHTING"
	SocketSwitch   ItemType = "SOCKET_SWITCH"
	Cabinet        ItemType = "CABINET"
	Countertop     ItemType = "COUNTERTOP"
	Sink           ItemType = "SINK"
	Faucet         ItemType = "FAUCET"
	ToiletFixture  ItemType = "TOILET_FIXTURE"
	ShowerBox      ItemType = "SHOWER_BOX"
	Bathtub        ItemType = "BATHTUB"
	Mirror         ItemType = "MIRROR"
	StoveCooktop   ItemType = "STOVE_COOKTOP"
	Oven           ItemType = "OVEN"
	Refrigerator   ItemType = "REFRIGERATOR"
	AirConditioner ItemType = "AIR_CONDITIONER"
	WaterHeater    ItemType = "WATER_HEATER"
	Wardrobe       ItemType = "WARDROBE"
	Bed            ItemType = "BED"
	Sofa           ItemType = "SOFA"
	Table          ItemType = "TABLE"
	Chair          ItemType = "CHAIR"
	CurtainBlind   ItemType = "CURTAIN_BLIND"
	ApplianceOther ItemType = "APPLIANCE_OTHER"
	FurnitureOther ItemType = "FURNITURE_OTHER"
	FixtureOther   ItemType = "FIXTURE_OTHER"
	OtherItem      ItemType = "OTHER"
)

const (
	New                    Condition = "NEW"
	Good                   Condition = "GOOD"
	Fair                   Condition = "FAIR"
	Poor                   Condition = "POOR"
	ConditionNotAssessable Condition = "NOT_ASSESSABLE"
)

const (
	Observed      Observation = "OBSERVED"
	Partial       Observation = "PARTIAL"
	NotObserved   Observation = "NOT_OBSERVED"
	NotAssessable Observation = "NOT_ASSESSABLE"
)

// EnvironmentTerm is one environment code with its pt-BR label.
type EnvironmentTerm struct {
	Code  EnvironmentType
	Label string
}

// ItemTerm is one item code with its pt-BR label.
type ItemTerm struct {
	Code  ItemType
	Label string
}

// ConditionDefinition describes one fixed inventory condition level.
type ConditionDefinition struct {
	Code        Condition
	Label       string
	Description string
}

var environmentTerms = []EnvironmentTerm{
	{LivingRoom, "Sala de estar"}, {DiningRoom, "Sala de jantar"}, {Kitchen, "Cozinha"}, {Bedroom, "Quarto"},
	{Suite, "Suíte"}, {Bathroom, "Banheiro"}, {Toilet, "Lavabo"}, {Laundry, "Lavanderia"},
	{ServiceArea, "Área de serviço"}, {Balcony, "Varanda"}, {Garage, "Garagem"}, {Hallway, "Corredor"},
	{Entrance, "Entrada"}, {Office, "Escritório"}, {Storage, "Depósito"}, {ExternalArea, "Área externa"},
	{PoolArea, "Área da piscina"}, {GourmetArea, "Área gourmet"}, {OtherEnvironment, "Outro"},
}

var itemTerms = []ItemTerm{
	{Floor, "Piso"}, {Wall, "Parede"}, {Ceiling, "Teto"}, {Door, "Porta"}, {Window, "Janela"},
	{Lighting, "Iluminação"}, {SocketSwitch, "Tomada ou interruptor"}, {Cabinet, "Armário"}, {Countertop, "Bancada"},
	{Sink, "Pia"}, {Faucet, "Torneira"}, {ToiletFixture, "Louça sanitária"}, {ShowerBox, "Box"}, {Bathtub, "Banheira"},
	{Mirror, "Espelho"}, {StoveCooktop, "Fogão ou cooktop"}, {Oven, "Forno"}, {Refrigerator, "Geladeira"},
	{AirConditioner, "Ar-condicionado"}, {WaterHeater, "Aquecedor de água"}, {Wardrobe, "Guarda-roupa"}, {Bed, "Cama"},
	{Sofa, "Sofá"}, {Table, "Mesa"}, {Chair, "Cadeira"}, {CurtainBlind, "Cortina ou persiana"},
	{ApplianceOther, "Outro eletrodoméstico"}, {FurnitureOther, "Outro móvel"}, {FixtureOther, "Outro acabamento ou instalação"},
	{OtherItem, "Outro"},
}

// conditionScale is ordered from best to worst; NOT_ASSESSABLE is outside the order.
var conditionScale = []ConditionDefinition{
	{New, "Novo", "Sem sinais de uso"},
	{Good, "Bom", "Uso leve, sem danos visíveis"},
	{Fair, "Regular", "Desgaste ou danos pequenos visíveis que não comprometem o uso"},
	{Poor, "Ruim", "Danos visíveis relevantes ou comprometimento aparente do uso"},
	{ConditionNotAssessable, "Não avaliável", "Evidência insuficiente para avaliar"},
}

var observations = []Observation{Observed, Partial, NotObserved, NotAssessable}

// EnvironmentTypes returns the environment vocabulary in display order.
func EnvironmentTypes() []EnvironmentTerm { return append([]EnvironmentTerm(nil), environmentTerms...) }

// ItemTypes returns the item vocabulary in display order.
func ItemTypes() []ItemTerm { return append([]ItemTerm(nil), itemTerms...) }

// Conditions returns the fixed condition scale from best to not assessable.
func Conditions() []ConditionDefinition { return append([]ConditionDefinition(nil), conditionScale...) }

// Observations returns the observation states.
func Observations() []Observation { return append([]Observation(nil), observations...) }

// EnvironmentLabel returns the pt-BR label of a known environment code.
func EnvironmentLabel(code string) (string, bool) {
	for _, term := range environmentTerms {
		if string(term.Code) == code {
			return term.Label, true
		}
	}
	return "", false
}

// ItemLabel returns the pt-BR label of a known item code.
func ItemLabel(code string) (string, bool) {
	for _, term := range itemTerms {
		if string(term.Code) == code {
			return term.Label, true
		}
	}
	return "", false
}

// IsEnvironmentType reports whether code belongs to the environment vocabulary.
func IsEnvironmentType(code string) bool { _, ok := EnvironmentLabel(code); return ok }

// IsItemType reports whether code belongs to the item vocabulary.
func IsItemType(code string) bool { _, ok := ItemLabel(code); return ok }

// IsCondition reports whether code is a level of the fixed scale.
func IsCondition(code string) bool {
	return conditionRank(code) >= 0 || code == string(ConditionNotAssessable)
}

// IsObservation reports whether code is a known observation state.
func IsObservation(code string) bool {
	for _, observation := range observations {
		if string(observation) == code {
			return true
		}
	}
	return false
}

// ItemPatch contains optional editable inventory attributes.
type ItemPatch struct {
	EnvironmentID *identity.ID
	TypeCode      *string
	Name          *string
	Material      *string
	Color         *string
	Quantity      *int
	// ClearQuantity makes the quantity unknown; it excludes Quantity.
	ClearQuantity bool
	Condition     *string
	Observation   *string
	Limitation    *string
}

// ConditionComparison is the direction of a condition change.
type ConditionComparison string

const (
	ConditionWorsened      ConditionComparison = "WORSENED"
	ConditionImproved      ConditionComparison = "IMPROVED"
	ConditionUnchanged     ConditionComparison = "UNCHANGED"
	ConditionNotComparable ConditionComparison = "NOT_COMPARABLE"
)

// ValidateName enforces the printable 1–80 rune inventory name contract.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > MaxNameRunes || !printable(name) {
		return apperror.New(apperror.InvalidInput, "name", "invalid name")
	}
	return nil
}

// ValidateEnvironment validates the environment code and display name.
// Every environment, OTHER included, needs a name.
func ValidateEnvironment(typeCode, name string) error {
	if !IsEnvironmentType(typeCode) {
		return apperror.New(apperror.InvalidInput, "typeCode", "unknown environment type")
	}
	return ValidateName(name)
}

// ValidateItem validates the item code and display name of a new item.
func ValidateItem(typeCode, name string) error {
	if !IsItemType(typeCode) {
		return apperror.New(apperror.InvalidInput, "typeCode", "unknown item type")
	}
	return ValidateName(name)
}

// ValidateItemPatch validates only supplied item attributes; nil means unknown.
// Fields are checked in a fixed order so the reported field is deterministic.
func ValidateItemPatch(patch ItemPatch) error {
	if patch.TypeCode != nil && !IsItemType(*patch.TypeCode) {
		return apperror.New(apperror.InvalidInput, "typeCode", "unknown item type")
	}
	if patch.Name != nil {
		if err := ValidateName(*patch.Name); err != nil {
			return err
		}
	}
	if patch.Material != nil && !validAttribute(*patch.Material, MaxMaterialRunes) {
		return apperror.New(apperror.InvalidInput, "material", "invalid material")
	}
	if patch.Color != nil && !validAttribute(*patch.Color, MaxColorRunes) {
		return apperror.New(apperror.InvalidInput, "color", "invalid color")
	}
	if patch.Quantity != nil && (*patch.Quantity < MinQuantity || *patch.Quantity > MaxQuantity) {
		return apperror.New(apperror.InvalidInput, "quantity", "quantity must be between 1 and 999")
	}
	if patch.ClearQuantity && patch.Quantity != nil {
		return apperror.New(apperror.InvalidInput, "quantity", "quantity cannot be set and cleared together")
	}
	if patch.Condition != nil && !IsCondition(*patch.Condition) {
		return apperror.New(apperror.InvalidInput, "condition", "unknown condition")
	}
	if patch.Observation != nil && !IsObservation(*patch.Observation) {
		return apperror.New(apperror.InvalidInput, "observation", "unknown observation")
	}
	if patch.Limitation != nil && !validAttribute(*patch.Limitation, MaxLimitationRunes) {
		return apperror.New(apperror.InvalidInput, "limitation", "invalid limitation")
	}
	return nil
}

// CompareCondition compares two values on the fixed scale; NOT_ASSESSABLE is incomparable.
func CompareCondition(before, after string) ConditionComparison {
	b, a := conditionRank(before), conditionRank(after)
	if b < 0 || a < 0 {
		return ConditionNotComparable
	}
	if a > b {
		return ConditionWorsened
	}
	if a < b {
		return ConditionImproved
	}
	return ConditionUnchanged
}

// conditionRank orders NEW > GOOD > FAIR > POOR; other values return -1.
func conditionRank(code string) int {
	for rank, level := range []Condition{New, Good, Fair, Poor} {
		if string(level) == code {
			return rank
		}
	}
	return -1
}

func validAttribute(value string, maxRunes int) bool {
	return utf8.RuneCountInString(value) <= maxRunes && printable(value)
}

func printable(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
