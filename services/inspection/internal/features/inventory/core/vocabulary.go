package core

import inventoryvocabulary "inspection/services/inspection/internal/features/inventory/vocabulary"

const (
	MaxEnvironments        = inventoryvocabulary.MaxEnvironments
	MaxItemsPerEnvironment = inventoryvocabulary.MaxItemsPerEnvironment
	MaxItemsPerVersion     = inventoryvocabulary.MaxItemsPerVersion
	MaxNameRunes           = inventoryvocabulary.MaxNameRunes
	MaxMaterialRunes       = inventoryvocabulary.MaxMaterialRunes
	MaxColorRunes          = inventoryvocabulary.MaxColorRunes
	MaxLimitationRunes     = inventoryvocabulary.MaxLimitationRunes
	MinQuantity            = inventoryvocabulary.MinQuantity
	MaxQuantity            = inventoryvocabulary.MaxQuantity
	LivingRoom             = inventoryvocabulary.LivingRoom
	DiningRoom             = inventoryvocabulary.DiningRoom
	Kitchen                = inventoryvocabulary.Kitchen
	Bedroom                = inventoryvocabulary.Bedroom
	Suite                  = inventoryvocabulary.Suite
	Bathroom               = inventoryvocabulary.Bathroom
	Toilet                 = inventoryvocabulary.Toilet
	Laundry                = inventoryvocabulary.Laundry
	ServiceArea            = inventoryvocabulary.ServiceArea
	Balcony                = inventoryvocabulary.Balcony
	Garage                 = inventoryvocabulary.Garage
	Hallway                = inventoryvocabulary.Hallway
	Entrance               = inventoryvocabulary.Entrance
	Office                 = inventoryvocabulary.Office
	Storage                = inventoryvocabulary.Storage
	ExternalArea           = inventoryvocabulary.ExternalArea
	PoolArea               = inventoryvocabulary.PoolArea
	GourmetArea            = inventoryvocabulary.GourmetArea
	OtherEnvironment       = inventoryvocabulary.OtherEnvironment
	Floor                  = inventoryvocabulary.Floor
	Wall                   = inventoryvocabulary.Wall
	Ceiling                = inventoryvocabulary.Ceiling
	Door                   = inventoryvocabulary.Door
	Window                 = inventoryvocabulary.Window
	Lighting               = inventoryvocabulary.Lighting
	SocketSwitch           = inventoryvocabulary.SocketSwitch
	Cabinet                = inventoryvocabulary.Cabinet
	Countertop             = inventoryvocabulary.Countertop
	Sink                   = inventoryvocabulary.Sink
	Faucet                 = inventoryvocabulary.Faucet
	ToiletFixture          = inventoryvocabulary.ToiletFixture
	ShowerBox              = inventoryvocabulary.ShowerBox
	Bathtub                = inventoryvocabulary.Bathtub
	Mirror                 = inventoryvocabulary.Mirror
	StoveCooktop           = inventoryvocabulary.StoveCooktop
	Oven                   = inventoryvocabulary.Oven
	Refrigerator           = inventoryvocabulary.Refrigerator
	AirConditioner         = inventoryvocabulary.AirConditioner
	WaterHeater            = inventoryvocabulary.WaterHeater
	Wardrobe               = inventoryvocabulary.Wardrobe
	Bed                    = inventoryvocabulary.Bed
	Sofa                   = inventoryvocabulary.Sofa
	Table                  = inventoryvocabulary.Table
	Chair                  = inventoryvocabulary.Chair
	CurtainBlind           = inventoryvocabulary.CurtainBlind
	ApplianceOther         = inventoryvocabulary.ApplianceOther
	FurnitureOther         = inventoryvocabulary.FurnitureOther
	FixtureOther           = inventoryvocabulary.FixtureOther
	OtherItem              = inventoryvocabulary.OtherItem
	New                    = inventoryvocabulary.New
	Good                   = inventoryvocabulary.Good
	Fair                   = inventoryvocabulary.Fair
	Poor                   = inventoryvocabulary.Poor
	ConditionNotAssessable = inventoryvocabulary.ConditionNotAssessable
	Observed               = inventoryvocabulary.Observed
	Partial                = inventoryvocabulary.Partial
	NotObserved            = inventoryvocabulary.NotObserved
	NotAssessable          = inventoryvocabulary.NotAssessable
)

type EnvironmentType = inventoryvocabulary.EnvironmentType
type ItemType = inventoryvocabulary.ItemType
type Condition = inventoryvocabulary.Condition
type Observation = inventoryvocabulary.Observation
type EnvironmentTerm = inventoryvocabulary.EnvironmentTerm
type ItemTerm = inventoryvocabulary.ItemTerm
type ConditionDefinition = inventoryvocabulary.ConditionDefinition
type ItemPatch = inventoryvocabulary.ItemPatch
type ConditionComparison = inventoryvocabulary.ConditionComparison

const (
	ConditionWorsened      = inventoryvocabulary.ConditionWorsened
	ConditionImproved      = inventoryvocabulary.ConditionImproved
	ConditionUnchanged     = inventoryvocabulary.ConditionUnchanged
	ConditionNotComparable = inventoryvocabulary.ConditionNotComparable
)

func EnvironmentTypes() []EnvironmentTerm         { return inventoryvocabulary.EnvironmentTypes() }
func ItemTypes() []ItemTerm                       { return inventoryvocabulary.ItemTypes() }
func Conditions() []ConditionDefinition           { return inventoryvocabulary.Conditions() }
func Observations() []Observation                 { return inventoryvocabulary.Observations() }
func EnvironmentLabel(code string) (string, bool) { return inventoryvocabulary.EnvironmentLabel(code) }
func ItemLabel(code string) (string, bool)        { return inventoryvocabulary.ItemLabel(code) }
func IsEnvironmentType(code string) bool          { return inventoryvocabulary.IsEnvironmentType(code) }
func IsItemType(code string) bool                 { return inventoryvocabulary.IsItemType(code) }
func IsCondition(code string) bool                { return inventoryvocabulary.IsCondition(code) }
func IsObservation(code string) bool              { return inventoryvocabulary.IsObservation(code) }
func ValidateName(name string) error              { return inventoryvocabulary.ValidateName(name) }
func ValidateEnvironment(typeCode, name string) error {
	return inventoryvocabulary.ValidateEnvironment(typeCode, name)
}
func ValidateItem(typeCode, name string) error {
	return inventoryvocabulary.ValidateItem(typeCode, name)
}
func ValidateItemPatch(patch ItemPatch) error { return inventoryvocabulary.ValidateItemPatch(patch) }
func CompareCondition(before, after string) ConditionComparison {
	return inventoryvocabulary.CompareCondition(before, after)
}
